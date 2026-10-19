package sources

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/versions"
)

// fixtures maps a URL to the payload a test wants that provider to see.
type fixtures map[string]string

func fetcherFor(t *testing.T, fx fixtures, ttl time.Duration) *Fetcher {
	t.Helper()
	return NewFetcher("", ttl, func(url string) ([]byte, error) {
		if body, ok := fx[url]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("no fixture for " + url)
	})
}

func TestGithubAtomStableAndPre(t *testing.T) {
	fx := fixtures{
		"https://github.com/example/proj/releases.atom": `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Release notes from proj</title>
  <entry><title>v2.1.0-rc.1</title></entry>
  <entry><title>v2.0.5</title></entry>
  <entry><title>v2.0.4</title></entry>
</feed>`,
	}
	p := Product{ID: "proj", Kind: "github-atom", Params: map[string]string{"repo": "example/proj"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "v2.0.5" {
		t.Errorf("Stable = %q, want v2.0.5", l.Stable)
	}
	if l.Pre != "v2.1.0-rc.1" {
		t.Errorf("Pre = %q, want v2.1.0-rc.1", l.Pre)
	}
}

func TestGithubAtomOnlyPreReleases(t *testing.T) {
	fx := fixtures{
		"https://github.com/example/proj/releases.atom": `<feed><entry><title>v1.0.0-beta.2</title></entry></feed>`,
	}
	p := Product{ID: "proj", Kind: "github-atom", Params: map[string]string{"repo": "example/proj"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "" {
		t.Errorf("Stable = %q, want empty: a beta is not a stable release", l.Stable)
	}
	if l.Pre != "v1.0.0-beta.2" {
		t.Errorf("Pre = %q, want v1.0.0-beta.2", l.Pre)
	}
}

// When the feed has no stable release, the API can still know one: its
// /releases/latest endpoint answers exactly that question.
func TestGithubAtomFallsBackToAPIWhenFeedHasNoStable(t *testing.T) {
	fx := fixtures{
		"https://github.com/example/proj/releases.atom": `<feed>
  <entry><title>build-2.5.20-dev-1642</title></entry>
  <entry><title>build-2.5.20-dev-1641</title></entry>
</feed>`,
		"https://api.github.com/repos/example/proj/releases": `[
  {"tag_name":"build-2.5.20-dev-1642","prerelease":true},
  {"tag_name":"build-2.5.20-dev-1641","prerelease":true}
]`,
		"https://api.github.com/repos/example/proj/releases/latest": `{"tag_name":"v2.4.21","prerelease":false}`,
	}
	p := Product{ID: "kotlin", Kind: "github-atom", Params: map[string]string{"repo": "example/proj"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "v2.4.21" {
		t.Errorf("Stable = %q, want v2.4.21 from /releases/latest", l.Stable)
	}
	if l.Pre != "build-2.5.20-dev-1642" {
		t.Errorf("Pre = %q, want the newest dev build from the feed", l.Pre)
	}
}

// A page of /releases can be entirely pre-releases even when stable releases
// exist further down, so an empty stable must trigger the /releases/latest call.
func TestGithubAPIAvoidsPromotingPreReleases(t *testing.T) {
	fx := fixtures{
		"https://api.github.com/repos/example/proj/releases": `[
  {"tag_name":"v3.1.0-rc.2","prerelease":true},
  {"tag_name":"v3.1.0-rc.1","prerelease":true}
]`,
		"https://api.github.com/repos/example/proj/releases/latest": `{"tag_name":"v3.0.0","prerelease":false}`,
	}
	p := Product{ID: "proj", Kind: "github-atom", Params: map[string]string{
		"repo": "example/proj", "api": "https://api.github.com/repos/example/proj/releases",
	}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "v3.0.0" {
		t.Errorf("Stable = %q, want v3.0.0", l.Stable)
	}
	if l.Pre != "v3.1.0-rc.2" {
		t.Errorf("Pre = %q, want v3.1.0-rc.2", l.Pre)
	}
}

// With no stable release anywhere, say so rather than inventing one.
func TestGithubAPIWithOnlyPreReleasesReportsNoStable(t *testing.T) {
	fx := fixtures{
		"https://api.github.com/repos/example/proj/releases": `[
  {"tag_name":"v3.1.0-rc.2","prerelease":true}
]`,
	}
	p := Product{ID: "proj", Kind: "github-atom", Params: map[string]string{
		"repo": "example/proj", "api": "https://api.github.com/repos/example/proj/releases",
	}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "" {
		t.Errorf("Stable = %q, want empty", l.Stable)
	}
	if l.Pre != "v3.1.0-rc.2" {
		t.Errorf("Pre = %q, want v3.1.0-rc.2", l.Pre)
	}
}

// The python.org FTP index carries entries like "win32" beside real releases.
func TestPythonFTPIgnoresNonVersionDirectories(t *testing.T) {
	fx := fixtures{
		"https://www.python.org/ftp/python/": `<html><body>
  <a href="../">../</a>
  <a href="win32/">win32/</a>
  <a href="3.14.7/">3.14.7/</a>
  <a href="3.14.6/">3.14.6/</a>
  <a href="3.15.0a1/">3.15.0a1/</a>
  <a href="2.7.18/">2.7.18/</a>
</body></html>`,
	}
	l := FetchLatest(Products["python"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "3.14.7" {
		t.Errorf("Stable = %q, want 3.14.7 (not win32)", l.Stable)
	}
	if l.Pre != "3.15.0a1" {
		t.Errorf("Pre = %q, want 3.15.0a1", l.Pre)
	}
}

func TestGithubAPIFallback(t *testing.T) {
	fx := fixtures{
		"https://api.github.com/repos/example/proj/releases": `[
		  {"tag_name":"v3.0.0","prerelease":false},
		  {"tag_name":"v3.1.0-rc.2","prerelease":true}
		]`,
	}
	p := Product{ID: "proj", Kind: "github-atom", Params: map[string]string{
		"repo": "example/proj", "api": "https://api.github.com/repos/example/proj/releases",
	}}
	// The atom URL has no fixture, so the provider must fall back to the API.
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "v3.0.0" {
		t.Errorf("Stable = %q, want v3.0.0", l.Stable)
	}
	if l.Pre != "v3.1.0-rc.2" {
		t.Errorf("Pre = %q, want v3.1.0-rc.2", l.Pre)
	}
}

func TestGoJSON(t *testing.T) {
	fx := fixtures{
		"https://go.dev/dl/?mode=json&include=all": `[
		  {"version":"go1.24rc1","stable":false},
		  {"version":"go1.23.4","stable":true},
		  {"version":"go1.23.3","stable":true}
		]`,
	}
	l := FetchLatest(Products["go"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "go1.23.4" {
		t.Errorf("Stable = %q, want go1.23.4", l.Stable)
	}
	if l.Pre != "go1.24rc1" {
		t.Errorf("Pre = %q, want go1.24rc1", l.Pre)
	}
}

func TestNodeLTSVsCurrent(t *testing.T) {
	fx := fixtures{
		"https://nodejs.org/dist/index.json": `[
		  {"version":"v23.1.0","lts":false},
		  {"version":"v22.10.0","lts":"Jod"},
		  {"version":"v20.18.0","lts":"Iron"}
		]`,
		"https://nodejs.org/download/rc/index.json": `[{"version":"v24.0.0-rc.1","lts":false}]`,
	}
	l := FetchLatest(Products["node"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "v22.10.0" {
		t.Errorf("Stable = %q, want the newest LTS v22.10.0", l.Stable)
	}
	if l.Current != "v23.1.0" {
		t.Errorf("Current = %q, want v23.1.0", l.Current)
	}
	if l.Pre != "v24.0.0-rc.1" {
		t.Errorf("Pre = %q, want v24.0.0-rc.1", l.Pre)
	}
}

func TestRustChannels(t *testing.T) {
	fx := fixtures{
		"https://static.rust-lang.org/dist/channel-rust-stable.toml": "manifest-version = \"2\"\n\n[pkg.cargo]\nversion = \"0.81.0\"\n\n[pkg.rust]\nversion = \"1.79.0 (129f3b996 2024-06-10)\"\n",
		"https://static.rust-lang.org/dist/channel-rust-beta.toml":   "manifest-version = \"2\"\n\n[pkg.rust]\nversion = \"1.80.0-beta.3 (abc 2024-07-01)\"\n",
	}
	l := FetchLatest(Products["rustc"], fetcherFor(t, fx, time.Hour))
	if !strings.HasPrefix(l.Stable, "1.79.0") {
		t.Errorf("Stable = %q, want 1.79.0", l.Stable)
	}
	if !strings.HasPrefix(l.Pre, "1.80.0-beta.3") {
		t.Errorf("Pre = %q, want 1.80.0-beta.3", l.Pre)
	}
}

func TestZigMasterIsPre(t *testing.T) {
	fx := fixtures{
		"https://ziglang.org/download/index.json": `{
		  "master": {"version":"0.14.0-dev.1234+abcdef"},
		  "0.13.0": {"version":"0.13.0"},
		  "0.12.0": {"version":"0.12.0"}
		}`,
	}
	l := FetchLatest(Products["zig"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "0.13.0" {
		t.Errorf("Stable = %q, want 0.13.0", l.Stable)
	}
	if l.Pre != "0.14.0-dev.1234+abcdef" {
		t.Errorf("Pre = %q, want the master version", l.Pre)
	}
}

func TestAdoptiumLtsCurrentTip(t *testing.T) {
	fx := fixtures{
		"https://api.adoptium.net/v3/info/available_releases": `{
		  "available_releases": [17, 21, 22],
		  "most_recent_feature_release": 22,
		  "most_recent_lts": 21,
		  "tip_version": 23
		}`,
	}
	l := FetchLatest(Products["java"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "21" {
		t.Errorf("Stable = %q, want the LTS 21", l.Stable)
	}
	if l.Current != "22" {
		t.Errorf("Current = %q, want 22", l.Current)
	}
	if l.Pre != "23" {
		t.Errorf("Pre = %q, want the tip 23", l.Pre)
	}
}

func TestDotnetPhases(t *testing.T) {
	fx := fixtures{
		"https://raw.githubusercontent.com/dotnet/core/main/release-notes/releases-index.json": `{
		  "releases-index": [
		    {"channel-version":"9.0","support-phase":"preview","latest-release":"9.0.0-preview.5"},
		    {"channel-version":"8.0","support-phase":"active","latest-release":"8.0.10"},
		    {"channel-version":"6.0","support-phase":"eol","latest-release":"6.0.36"}
		  ]
		}`,
	}
	l := FetchLatest(Products["dotnet"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "8.0.10" {
		t.Errorf("Stable = %q, want 8.0.10", l.Stable)
	}
	if l.Pre != "9.0.0-preview.5" {
		t.Errorf("Pre = %q, want 9.0.0-preview.5", l.Pre)
	}
}

func TestPythonFTPIndex(t *testing.T) {
	fx := fixtures{
		"https://www.python.org/ftp/python/": `<html><body>
		  <a href="3.13.0/">3.13.0/</a>
		  <a href="3.14.0a2/">3.14.0a2/</a>
		  <a href="3.12.7/">3.12.7/</a>
		</body></html>`,
	}
	l := FetchLatest(Products["python"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "3.13.0" {
		t.Errorf("Stable = %q, want 3.13.0", l.Stable)
	}
	if l.Pre != "3.14.0a2" {
		t.Errorf("Pre = %q, want 3.14.0a2", l.Pre)
	}
}

func TestGccFTPIndex(t *testing.T) {
	fx := fixtures{
		"https://ftp.gnu.org/gnu/gcc/": `<html><body>
		  <a href="gcc-14.2.0/">gcc-14.2.0/</a>
		  <a href="gcc-13.3.0/">gcc-13.3.0/</a>
		  <a href="gcc-15-20250101/">gcc-15-20250101/</a>
		</body></html>`,
	}
	l := FetchLatest(Products["gcc"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "14.2.0" {
		t.Errorf("Stable = %q, want 14.2.0", l.Stable)
	}
}

func TestDartVersions(t *testing.T) {
	fx := fixtures{
		"https://storage.googleapis.com/dart-archive/channels/stable/release/latest/VERSION": `{"version":"3.5.4","revision":"abc"}`,
		"https://storage.googleapis.com/dart-archive/channels/beta/release/latest/VERSION":   `{"version":"3.6.0-1.2.beta","revision":"def"}`,
	}
	l := FetchLatest(Products["dart"], fetcherFor(t, fx, time.Hour))
	if l.Stable != "3.5.4" {
		t.Errorf("Stable = %q, want 3.5.4", l.Stable)
	}
	if l.Pre != "3.6.0-1.2.beta" {
		t.Errorf("Pre = %q, want 3.6.0-1.2.beta", l.Pre)
	}
}

func TestProviderFailureIsUnknown(t *testing.T) {
	for _, id := range []string{"go", "node", "rustc", "zig", "java", "dotnet", "python", "gcc", "dart", "clang"} {
		l := FetchLatest(Products[id], fetcherFor(t, fixtures{}, time.Hour))
		if !strings.HasPrefix(l.Source, "error") {
			t.Errorf("%s: Source = %q, want it to start with error", id, l.Source)
		}
		state, _, _ := versions.Judge("1.0.0", l, "stable")
		if state != "unknown" {
			t.Errorf("%s: Judge on a failed provider = %q, want unknown", id, state)
		}
	}
}

func TestProviderNeverPanicsOnMalformedPayload(t *testing.T) {
	fx := fixtures{}
	for _, p := range Products {
		for _, u := range urlsOf(p) {
			fx[u] = `{"not":"what we expected"`
		}
	}
	f := fetcherFor(t, fx, time.Hour)
	for id, p := range Products {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked on a malformed payload: %v", id, r)
				}
			}()
			FetchLatest(p, f)
		}()
	}
}

func TestResultCacheTTLAndStaleOnFailure(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "fetch.json")
	calls := 0
	offline := false
	get := func(url string) ([]byte, error) {
		calls++
		if offline {
			return nil, errors.New("offline")
		}
		return []byte("BODY"), nil
	}

	f := NewFetcher(cachePath, time.Hour, get)
	if s, err := f.Text("http://x"); err != nil || s != "BODY" {
		t.Fatalf("first fetch = (%q,%v)", s, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if _, err := f.Text("http://x"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (served from cache within TTL)", calls)
	}

	offline = true
	stale := NewFetcher(cachePath, 0, get)
	if s, err := stale.Text("http://x"); err != nil || s != "BODY" {
		t.Fatalf("expired cache did not fall back to the stored body: (%q,%v)", s, err)
	}

	fresh := NewFetcher(cachePath, time.Hour, func(string) ([]byte, error) { return nil, errors.New("offline") })
	if _, err := fresh.Text("http://unknown"); err == nil {
		t.Fatal("a URL with no cache and no network returned no error")
	}
}

func TestUpgradeHintByOrigin(t *testing.T) {
	inst := detect.Install{Tool: "node", Path: "/usr/local/bin/node", Origin: "homebrew", OK: true, Version: "20.0.0"}
	hint := UpgradeHint(Products["node"], inst, "macos", false)
	if !strings.Contains(hint, "brew") {
		t.Errorf("homebrew hint = %q, want it to mention brew", hint)
	}
	inst.Origin = "system"
	inst.Path = "/usr/bin/node"
	hint = UpgradeHint(Products["node"], inst, "linux", false)
	if hint == "" {
		t.Error("system upgrade hint is empty")
	}
}

func TestProductForTool(t *testing.T) {
	if _, ok := ProductForTool("node"); !ok {
		t.Error("ProductForTool(node) not found")
	}
	if _, ok := ProductForTool("nosuchtool"); ok {
		t.Error("ProductForTool(nosuchtool) found something")
	}
	if len(Products) < 15 {
		t.Errorf("only %d products defined, want the full set", len(Products))
	}
}
