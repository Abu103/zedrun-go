// Package sources reports the latest release of each product from its upstream
// endpoint. Every provider is defensive: a failure, an offline machine or a
// malformed payload yields a Latest whose Source starts with "error", never a
// panic. Responses are cached on disk and a stale entry is used when the
// network fails.
package sources

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/versions"
)

// Product is one upstream project whose releases zedrun-go can check.
type Product struct {
	ID     string
	Tools  []string
	Kind   string
	Params map[string]string
	Pkg    string // package name for upgrade hints
}

// Products maps a product id to its provider configuration.
var Products = map[string]Product{
	"clang":  {ID: "clang", Tools: []string{"clang", "clang++"}, Kind: "github-atom", Params: map[string]string{"repo": "llvm/llvm-project"}, Pkg: "llvm"},
	"deno":   {ID: "deno", Tools: []string{"deno"}, Kind: "github-atom", Params: map[string]string{"repo": "denoland/deno"}, Pkg: "deno"},
	"bun":    {ID: "bun", Tools: []string{"bun"}, Kind: "github-atom", Params: map[string]string{"repo": "oven-sh/bun"}, Pkg: "bun"},
	"julia":  {ID: "julia", Tools: []string{"julia"}, Kind: "github-atom", Params: map[string]string{"repo": "JuliaLang/julia"}, Pkg: "julia"},
	"kotlin": {ID: "kotlin", Tools: []string{"kotlin"}, Kind: "github-atom", Params: map[string]string{"repo": "JetBrains/kotlin"}, Pkg: "kotlin"},
	"scala":  {ID: "scala", Tools: []string{"scala"}, Kind: "github-atom", Params: map[string]string{"repo": "scala/scala3"}, Pkg: "scala"},
	"elixir": {ID: "elixir", Tools: []string{"elixir"}, Kind: "github-atom", Params: map[string]string{"repo": "elixir-lang/elixir"}, Pkg: "elixir"},
	"pwsh":   {ID: "pwsh", Tools: []string{"pwsh"}, Kind: "github-atom", Params: map[string]string{"repo": "PowerShell/PowerShell"}, Pkg: "powershell"},
	"go": {ID: "go", Tools: []string{"go"}, Kind: "go-json",
		Params: map[string]string{"url": "https://go.dev/dl/?mode=json&include=all"}, Pkg: "golang"},
	"node": {ID: "node", Tools: []string{"node"}, Kind: "node-json",
		Params: map[string]string{
			"url": "https://nodejs.org/dist/index.json",
			"rc":  "https://nodejs.org/download/rc/index.json",
		}, Pkg: "nodejs"},
	"rustc": {ID: "rustc", Tools: []string{"rustc", "cargo"}, Kind: "rust-channel", Pkg: "rust"},
	"zig":   {ID: "zig", Tools: []string{"zig"}, Kind: "zig-json", Params: map[string]string{"url": "https://ziglang.org/download/index.json"}, Pkg: "zig"},
	"java":  {ID: "java", Tools: []string{"java", "javac"}, Kind: "adoptium", Params: map[string]string{"url": "https://api.adoptium.net/v3/info/available_releases"}, Pkg: "temurin"},
	"dotnet": {ID: "dotnet", Tools: []string{"dotnet"}, Kind: "dotnet-index",
		Params: map[string]string{"url": "https://raw.githubusercontent.com/dotnet/core/main/release-notes/releases-index.json"}, Pkg: "dotnet-sdk"},
	"python": {ID: "python", Tools: []string{"python"}, Kind: "python-ftp", Params: map[string]string{"url": "https://www.python.org/ftp/python/"}, Pkg: "python"},
	"gcc":    {ID: "gcc", Tools: []string{"gcc", "g++"}, Kind: "gcc-ftp", Params: map[string]string{"url": "https://ftp.gnu.org/gnu/gcc/"}, Pkg: "gcc"},
	"dart": {ID: "dart", Tools: []string{"dart"}, Kind: "dart-version",
		Params: map[string]string{
			"stable": "https://storage.googleapis.com/dart-archive/channels/stable/release/latest/VERSION",
			"beta":   "https://storage.googleapis.com/dart-archive/channels/beta/release/latest/VERSION",
		}, Pkg: "dart"},
}

const (
	rustStableURL = "https://static.rust-lang.org/dist/channel-rust-stable.toml"
	rustBetaURL   = "https://static.rust-lang.org/dist/channel-rust-beta.toml"
)

// ProductForTool finds the product that covers a tool id.
func ProductForTool(toolID string) (Product, bool) {
	keys := make([]string, 0, len(Products))
	for k := range Products {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, t := range Products[k].Tools {
			if t == toolID {
				return Products[k], true
			}
		}
	}
	return Product{}, false
}

// urlsOf lists every URL a product may request, so a caller can warm or check
// the endpoints.
func urlsOf(p Product) []string {
	switch p.Kind {
	case "github-atom":
		u := []string{"https://github.com/" + p.Params["repo"] + "/releases.atom"}
		if api := p.Params["api"]; api != "" {
			u = append(u, api)
		} else if p.Params["repo"] != "" {
			u = append(u, "https://api.github.com/repos/"+p.Params["repo"]+"/releases")
		}
		return u
	case "rust-channel":
		return []string{rustStableURL, rustBetaURL}
	case "node-json":
		return []string{p.Params["url"], p.Params["rc"]}
	case "dart-version":
		return []string{p.Params["stable"], p.Params["beta"]}
	default:
		if u := p.Params["url"]; u != "" {
			return []string{u}
		}
	}
	return nil
}

type fetchEntry struct {
	Time time.Time `json:"time"`
	Body string    `json:"body"`
}

// Fetcher performs HTTP GETs with an on-disk TTL cache. It never fails when a
// stale cache entry exists.
type Fetcher struct {
	cachePath string
	ttl       time.Duration
	httpGet   func(string) ([]byte, error)
	refresh   bool
	mem       map[string]fetchEntry
}

// NewFetcher builds a Fetcher. Pass nil for httpGet to use the real network.
func NewFetcher(cachePath string, ttl time.Duration, httpGet func(string) ([]byte, error)) *Fetcher {
	f := &Fetcher{cachePath: cachePath, ttl: ttl, httpGet: httpGet, mem: map[string]fetchEntry{}}
	if f.httpGet == nil {
		f.httpGet = defaultGet
	}
	if cachePath != "" {
		if data, err := os.ReadFile(cachePath); err == nil {
			var m map[string]fetchEntry
			if err := json.Unmarshal(data, &m); err == nil && m != nil {
				f.mem = m
			}
		}
	}
	return f
}

// SetRefresh forces the next fetch to hit the network.
func (f *Fetcher) SetRefresh(v bool) { f.refresh = v }

// Text returns the body of a URL, from cache when fresh.
func (f *Fetcher) Text(url string) (string, error) {
	if e, ok := f.mem[url]; ok && !f.refresh && time.Since(e.Time) < f.ttl {
		return e.Body, nil
	}
	body, err := f.httpGet(url)
	if err != nil {
		if e, ok := f.mem[url]; ok {
			return e.Body, nil // stale is better than nothing
		}
		return "", err
	}
	f.mem[url] = fetchEntry{Time: time.Now(), Body: string(body)}
	_ = f.Save()
	return string(body), nil
}

// JSON returns a URL's body parsed as JSON.
func (f *Fetcher) JSON(url string) (any, error) {
	body, err := f.Text(url)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Save writes the cache to disk.
func (f *Fetcher) Save() error {
	if f.cachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.cachePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f.mem, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.cachePath, data, 0o644)
}

func defaultGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zedrun-go")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "token "+tok)
	} else if tok := os.Getenv("GH_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "token "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%s: rate limited (set GITHUB_TOKEN to raise the limit)", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// FetchLatest reports the latest stable and pre-release versions of a product.
// It never returns an error: a failure comes back as Latest{Source: "error: …"}.
func FetchLatest(p Product, f *Fetcher) versions.Latest {
	defer func() {
		_ = recover() // a provider bug must never take the process down
	}()
	var l versions.Latest
	switch p.Kind {
	case "github-atom":
		l = githubAtom(p, f)
	case "go-json":
		l = goJSON(p, f)
	case "node-json":
		l = nodeJSON(p, f)
	case "rust-channel":
		l = rustChannel(p, f)
	case "zig-json":
		l = zigJSON(p, f)
	case "adoptium":
		l = adoptium(p, f)
	case "dotnet-index":
		l = dotnetIndex(p, f)
	case "python-ftp":
		l = pythonFTP(p, f)
	case "gcc-ftp":
		l = gccFTP(p, f)
	case "dart-version":
		l = dartVersion(p, f)
	default:
		l = versions.Latest{Source: "error: unknown provider kind " + p.Kind}
	}
	if l.Source == "" {
		l.Source = "error: provider returned nothing"
	}
	return l
}

type atomFeed struct {
	Entries []struct {
		Title string `xml:"title"`
	} `xml:"entry"`
}

func githubAtom(p Product, f *Fetcher) versions.Latest {
	repo := p.Params["repo"]
	url := "https://github.com/" + repo + "/releases.atom"
	body, err := f.Text(url)
	if err != nil {
		return githubAPI(p, f)
	}
	var feed atomFeed
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return versions.Latest{Source: "error: bad atom feed: " + err.Error()}
	}
	l := versions.Latest{Source: url}
	for _, e := range feed.Entries {
		tag := strings.TrimSpace(e.Title)
		if tag == "" {
			continue
		}
		if isPreTag(tag) {
			if l.Pre == "" {
				l.Pre = tag
			}
		} else if l.Stable == "" {
			l.Stable = tag
		}
		if l.Stable != "" && l.Pre != "" {
			break
		}
	}
	if l.Stable == "" && l.Pre == "" {
		return versions.Latest{Source: "error: no releases in the atom feed"}
	}
	if l.Stable == "" {
		l.Stable = l.Pre
		l.Pre = ""
	}
	return l
}

func githubAPI(p Product, f *Fetcher) versions.Latest {
	api := p.Params["api"]
	if api == "" {
		api = "https://api.github.com/repos/" + p.Params["repo"] + "/releases"
	}
	v, err := f.JSON(api)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	arr, ok := v.([]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected github payload"}
	}
	l := versions.Latest{Source: api}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		tag := str(m["tag_name"])
		if tag == "" {
			continue
		}
		pre, _ := m["prerelease"].(bool)
		if pre || isPreTag(tag) {
			if l.Pre == "" {
				l.Pre = tag
			}
		} else if l.Stable == "" {
			l.Stable = tag
		}
	}
	if l.Stable == "" && l.Pre == "" {
		return versions.Latest{Source: "error: no releases in the github payload"}
	}
	if l.Stable == "" {
		l.Stable = l.Pre
		l.Pre = ""
	}
	return l
}

func goJSON(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	v, err := f.JSON(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	arr, ok := v.([]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected go payload"}
	}
	l := versions.Latest{Source: url}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ver := str(m["version"])
		if ver == "" {
			continue
		}
		stable, _ := m["stable"].(bool)
		if stable {
			if l.Stable == "" {
				l.Stable = ver
			}
		} else if l.Pre == "" {
			l.Pre = ver
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no stable go release found"}
	}
	return l
}

func nodeJSON(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	v, err := f.JSON(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	arr, ok := v.([]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected node payload"}
	}
	l := versions.Latest{Source: url}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ver := str(m["version"])
		if ver == "" {
			continue
		}
		if l.Current == "" {
			l.Current = ver
		}
		lts, hasLTS := m["lts"].(string)
		if hasLTS && lts != "" && l.Stable == "" {
			l.Stable = ver
		}
	}
	if l.Stable == "" {
		l.Stable = l.Current
	}
	if rc := p.Params["rc"]; rc != "" {
		if v, err := f.JSON(rc); err == nil {
			if arr, ok := v.([]any); ok && len(arr) > 0 {
				if m, ok := arr[0].(map[string]any); ok {
					l.Pre = str(m["version"])
				}
			}
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no node release found"}
	}
	return l
}

func rustChannel(p Product, f *Fetcher) versions.Latest {
	l := versions.Latest{Source: rustStableURL}
	stable, err := f.Text(rustStableURL)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	l.Stable = rustVersionFromToml(stable)
	if beta, err := f.Text(rustBetaURL); err == nil {
		l.Pre = rustVersionFromToml(beta)
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no version in the rust channel manifest"}
	}
	return l
}

// rustVersionFromToml reads the version under [pkg.rust] so the cargo version is
// not mistaken for the toolchain version.
func rustVersionFromToml(text string) string {
	inRust := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inRust = t == "[pkg.rust]"
			continue
		}
		if inRust && strings.HasPrefix(t, "version") {
			if _, v, ok := strings.Cut(t, "="); ok {
				return strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	return ""
}

func zigJSON(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	v, err := f.JSON(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected zig payload"}
	}
	l := versions.Latest{Source: url}
	if master, ok := m["master"].(map[string]any); ok {
		l.Pre = str(master["version"])
	}
	for k, e := range m {
		if k == "master" {
			continue
		}
		if _, ok := versions.ParseVersion(k); !ok {
			continue
		}
		if l.Stable == "" || greater(k, l.Stable) {
			l.Stable = k
			_ = e
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no stable zig release found"}
	}
	return l
}

func adoptium(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	v, err := f.JSON(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected adoptium payload"}
	}
	lts := int(num(m["most_recent_lts"]))
	current := int(num(m["most_recent_feature_release"]))
	tip := int(num(m["tip_version"]))
	l := versions.Latest{Source: url}
	if lts > 0 {
		l.Stable = strconv.Itoa(lts)
	}
	if current > 0 {
		l.Current = strconv.Itoa(current)
	}
	if tip > current && tip > lts {
		l.Pre = strconv.Itoa(tip)
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no LTS release in the adoptium payload"}
	}
	return l
}

func dotnetIndex(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	v, err := f.JSON(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected dotnet payload"}
	}
	arr, ok := m["releases-index"].([]any)
	if !ok {
		return versions.Latest{Source: "error: no releases-index in the dotnet payload"}
	}
	l := versions.Latest{Source: url}
	for _, e := range arr {
		row, ok := e.(map[string]any)
		if !ok {
			continue
		}
		phase := str(row["support-phase"])
		rel := str(row["latest-release"])
		if rel == "" {
			continue
		}
		switch phase {
		case "preview", "go-live":
			if l.Pre == "" || greater(rel, l.Pre) {
				l.Pre = rel
			}
		case "eol":
			// end-of-life channels are not an upgrade target
		default:
			if l.Stable == "" || greater(rel, l.Stable) {
				l.Stable = rel
			}
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no supported dotnet release found"}
	}
	return l
}

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

func hrefs(body string) []string {
	var out []string
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func pythonFTP(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	body, err := f.Text(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	l := versions.Latest{Source: url}
	for _, h := range hrefs(body) {
		name := strings.Trim(h, "/")
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		v, ok := versions.ParseVersion(name)
		if !ok || v.Raw != name {
			continue
		}
		if versions.IsPrerelease(v) {
			if l.Pre == "" || greater(name, l.Pre) {
				l.Pre = name
			}
		} else if l.Stable == "" || greater(name, l.Stable) {
			l.Stable = name
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no stable python release found"}
	}
	return l
}

var gccDirRe = regexp.MustCompile(`^gcc-(\d+\.\d+(?:\.\d+)?)$`)

func gccFTP(p Product, f *Fetcher) versions.Latest {
	url := p.Params["url"]
	body, err := f.Text(url)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	l := versions.Latest{Source: url}
	for _, h := range hrefs(body) {
		name := strings.Trim(h, "/")
		m := gccDirRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if l.Stable == "" || greater(m[1], l.Stable) {
			l.Stable = m[1]
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no stable gcc release found"}
	}
	return l
}

func dartVersion(p Product, f *Fetcher) versions.Latest {
	stableURL := p.Params["stable"]
	v, err := f.JSON(stableURL)
	if err != nil {
		return versions.Latest{Source: "error: " + err.Error()}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return versions.Latest{Source: "error: unexpected dart payload"}
	}
	l := versions.Latest{Source: stableURL, Stable: str(m["version"])}
	if beta := p.Params["beta"]; beta != "" {
		if v, err := f.JSON(beta); err == nil {
			if m, ok := v.(map[string]any); ok {
				l.Pre = str(m["version"])
			}
		}
	}
	if l.Stable == "" {
		return versions.Latest{Source: "error: no dart version in the payload"}
	}
	return l
}

var preKeywords = []string{"alpha", "beta", "rc", "preview", "nightly", "dev", "insiders", "canary", "snapshot", "-ea", "pre"}

func isPreTag(tag string) bool {
	low := strings.ToLower(tag)
	for _, k := range preKeywords {
		if strings.Contains(low, k) {
			return true
		}
	}
	if v, ok := versions.ParseVersion(tag); ok {
		return versions.IsPrerelease(v)
	}
	return false
}

func greater(a, b string) bool {
	av, ok1 := versions.ParseVersion(a)
	bv, ok2 := versions.ParseVersion(b)
	if !ok1 {
		return false
	}
	if !ok2 {
		return true
	}
	return versions.Compare(av, bv) > 0
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

func num(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// upgradeHints turn an install origin into the command that upgrades it.
var upgradeHints = map[string]string{
	"homebrew":      "brew upgrade %s",
	"snap":          "snap refresh %s",
	"msys2":         "pacman -Syu %s (in the MSYS2 shell)",
	"chocolatey":    "choco upgrade %s",
	"scoop":         "scoop update %s",
	"pyenv":         "pyenv install --list (then pyenv global <version>)",
	"visual-studio": "run the Visual Studio Installer to update the MSVC toolset",
	"WindowsApps":   "winget upgrade %s",
	"WSL":           "winget upgrade %s (on Windows)",
	"rustup":        "rustup update",
	"linuxbrew":     "brew upgrade %s",
	"system":        "",
}

// UpgradeHint says how to update an installed tool, based on where it came from.
func UpgradeHint(p Product, inst detect.Install, osName string, pre bool) string {
	pkg := p.Pkg
	if pkg == "" {
		pkg = p.ID
	}
	if tmpl, ok := upgradeHints[inst.Origin]; ok && tmpl != "" {
		return fmt.Sprintf(tmpl, pkg)
	}
	switch osName {
	case "windows":
		return "winget upgrade " + pkg
	case "macos":
		return "brew upgrade " + pkg
	default:
		return "use your package manager to update " + pkg
	}
}

// EndpointResult is one row of a `doctor --sources` check.
type EndpointResult struct {
	Product string
	URL     string
	OK      bool
	Note    string
}

// CheckEndpoints probes every product's endpoint and reports which are alive.
func CheckEndpoints(f *Fetcher, ps map[string]Product) []EndpointResult {
	keys := make([]string, 0, len(ps))
	for k := range ps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []EndpointResult
	for _, k := range keys {
		p := ps[k]
		l := FetchLatest(p, f)
		ok := !strings.HasPrefix(l.Source, "error")
		note := ""
		if !ok {
			note = strings.TrimPrefix(l.Source, "error: ")
		} else if l.Stable != "" {
			note = "latest stable " + l.Stable
		}
		out = append(out, EndpointResult{Product: p.ID, URL: l.Source, OK: ok, Note: note})
	}
	return out
}
