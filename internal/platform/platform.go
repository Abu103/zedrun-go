// Package platform abstracts the operating system: directory layout, PATH
// scanning, executable resolution and origin classification. Every function
// takes an explicit env and OS name so Windows/macOS logic is testable on Linux.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// LowPriority origins sort last when several installs of one tool are found:
// they are usually shims or interop wrappers, not the real toolchain.
var LowPriority = map[string]bool{"WSL": true, "WindowsApps": true}

// OSName returns "windows", "macos" or "linux". A non-empty override is
// returned as-is so callers can exercise cross-OS behaviour on any host.
func OSName(osOverride string) string {
	if osOverride != "" {
		return osOverride
	}
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

func join(osName string, parts ...string) string {
	if osName == "windows" {
		return strings.Join(parts, `\`)
	}
	return strings.Join(parts, "/")
}

// Env reads an environment variable the way the platform does.
//
// Unix environment variables are case-sensitive — PATH and path are different
// variables — so the lookup there stays exact. Windows lookups are case
// insensitive, and the environment block keeps whatever casing the parent
// process used, so one machine hands us `Path`, another `PATH`, another `path`.
// An exact-case map lookup therefore finds nothing, which is how the whole PATH
// came to be skipped on Windows: python, go and node live only on it, so they
// were invisible.
func Env(env map[string]string, osOverride, name string) string {
	if v := env[name]; v != "" {
		return v
	}
	if OSName(osOverride) != "windows" {
		return ""
	}
	for k, v := range env {
		if v != "" && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func home(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := Env(env, osName, "USERPROFILE"); v != "" {
			return v
		}
		if d, p := Env(env, osName, "HOMEDRIVE"), Env(env, osName, "HOMEPATH"); d != "" || p != "" {
			return d + p
		}
	}
	return Env(env, osName, "HOME")
}

func appData(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := Env(env, osName, "APPDATA"); v != "" {
			return v
		}
	}
	return home(env, osName)
}

func localAppData(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := Env(env, osName, "LOCALAPPDATA"); v != "" {
			return v
		}
	}
	return appData(env, osName)
}

func xdg(env map[string]string, key, fallback string) string {
	if v := env[key]; v != "" {
		return v
	}
	return fallback
}

// ConfigDir is where zedrun-go keeps user configuration.
func ConfigDir(env map[string]string, osOverride string) string {
	o := OSName(osOverride)
	switch o {
	case "windows":
		return join(o, appData(env, o), "zedrun")
	case "macos":
		return join(o, home(env, o), "Library", "Application Support", "zedrun")
	default:
		return join(o, xdg(env, "XDG_CONFIG_HOME", join(o, home(env, o), ".config")), "zedrun")
	}
}

// CacheDir is where zedrun-go keeps build artifacts and the probe/version caches.
func CacheDir(env map[string]string, osOverride string) string {
	o := OSName(osOverride)
	switch o {
	case "windows":
		return join(o, localAppData(env, o), "zedrun")
	case "macos":
		return join(o, home(env, o), "Library", "Caches", "zedrun")
	default:
		return join(o, xdg(env, "XDG_CACHE_HOME", join(o, home(env, o), ".cache")), "zedrun")
	}
}

// DataDir is where zedrun-go keeps durable data, such as its own binary copy.
func DataDir(env map[string]string, osOverride string) string {
	o := OSName(osOverride)
	switch o {
	case "windows":
		return join(o, appData(env, o), "zedrun")
	case "macos":
		return join(o, home(env, o), "Library", "Application Support", "zedrun")
	default:
		return join(o, xdg(env, "XDG_DATA_HOME", join(o, home(env, o), ".local", "share")), "zedrun")
	}
}

// ZedConfigDir is Zed's own configuration directory. Windows uses the roaming
// AppData path (Zed's documented location), not ~/.config.
func ZedConfigDir(env map[string]string, osOverride string) string {
	o := OSName(osOverride)
	switch o {
	case "windows":
		return join(o, appData(env, o), "Zed")
	case "macos":
		return join(o, home(env, o), "Library", "Application Support", "Zed")
	default:
		return join(o, xdg(env, "XDG_CONFIG_HOME", join(o, home(env, o), ".config")), "zed")
	}
}

// PathEntries returns PATH entries for the OS with surrounding quotes removed
// and empty entries dropped.
func PathEntries(env map[string]string, osOverride string) []string {
	sep := ":"
	if OSName(osOverride) == "windows" {
		sep = ";"
	}
	var out []string
	for _, p := range strings.Split(Env(env, osOverride, "PATH"), sep) {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ExeExts returns the executable extensions to try, lowercased. POSIX has none.
func ExeExts(env map[string]string, osOverride string) []string {
	if OSName(osOverride) != "windows" {
		return []string{""}
	}
	raw := Env(env, osOverride, "PATHEXT")
	if raw == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, e := range strings.Split(raw, ";") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// FindInDir returns the existing, executable files in dir matching one of names
// with any OS executable extension, in name order. It stats each candidate.
func FindInDir(dir string, names []string, env map[string]string, osOverride string) []string {
	return FindInDirCached(dir, names, env, osOverride, nil)
}

// dirListing is one memoised directory listing: folded name -> name on disk.
type dirListing struct {
	byKey map[string]string
	ok    bool
}

// DirCache memoises directory listings. Probing one tool stats every candidate
// name in every search directory; probing thirty tools does it thirty times over,
// which on a WSL machine with a Windows PATH (dozens of /mnt entries) costs
// seconds. Listing each directory once and matching names in memory turns that
// into one readdir per directory per run.
//
// The cache is scoped to a run: a tool installed mid-run is not seen until the
// next one.
type DirCache struct {
	mu sync.Mutex
	m  map[string]dirListing
}

// NewDirCache returns an empty per-run listing cache.
func NewDirCache() *DirCache {
	return &DirCache{m: map[string]dirListing{}}
}

// List returns the regular-file entries of dir as folded-name -> real-name.
// fold lowercases the keys, for case-insensitive filesystems.
func (c *DirCache) List(dir string, fold bool) (map[string]string, bool) {
	c.mu.Lock()
	if l, ok := c.m[dir]; ok {
		c.mu.Unlock()
		return l.byKey, l.ok
	}
	c.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		c.mu.Lock()
		c.m[dir] = dirListing{ok: false}
		c.mu.Unlock()
		return nil, false
	}
	byKey := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		key := name
		if fold {
			key = strings.ToLower(name)
		}
		byKey[key] = name
	}
	c.mu.Lock()
	c.m[dir] = dirListing{byKey: byKey, ok: true}
	c.mu.Unlock()
	return byKey, true
}

// FindInDirCached is FindInDir backed by a shared listing cache. With a nil
// cache it behaves exactly like FindInDir.
func FindInDirCached(dir string, names []string, env map[string]string, osOverride string, dc *DirCache) []string {
	o := OSName(osOverride)
	fold := o == "windows"
	exts := ExeExts(env, o)

	if dc != nil {
		entries, ok := dc.List(dir, fold)
		if !ok {
			return nil
		}
		var out []string
		seen := map[string]bool{}
		for _, name := range names {
			for _, ext := range exts {
				key := name + ext
				if fold {
					key = strings.ToLower(key)
				}
				real, found := entries[key]
				if !found {
					continue
				}
				p := filepath.Join(dir, real)
				if seen[p] || !isExecutable(p, o) {
					continue
				}
				seen[p] = true
				out = append(out, p)
			}
		}
		return out
	}

	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		for _, ext := range exts {
			p := filepath.Join(dir, name+ext)
			if seen[p] {
				continue
			}
			if isExecutable(p, o) {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func isExecutable(p, osName string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if osName == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

// ExtraSearchDirs returns well-known install locations to probe beyond PATH.
// Entries need not exist; callers filter by existence.
func ExtraSearchDirs(env map[string]string, osOverride string) []string {
	o := OSName(osOverride)
	h := home(env, o)
	if o == "windows" {
		return []string{
			`C:\msys64\ucrt64\bin`,
			`C:\msys64\mingw64\bin`,
			`C:\msys64\clang64\bin`,
			`C:\Program Files\LLVM\bin`,
			`C:\Program Files\Git\bin`,
			`C:\Program Files\Git\usr\bin`,
			`C:\ProgramData\chocolatey\bin`,
			join(o, h, "scoop", "shims"),
			join(o, h, ".cargo", "bin"),
			join(o, h, ".bun", "bin"),
			join(o, h, ".deno", "bin"),
			join(o, h, "AppData", "Local", "Microsoft", "WinGet", "Links"),
		}
	}
	return []string{
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/opt/homebrew/bin",
		"/opt/homebrew/opt/llvm/bin",
		"/usr/local/opt/llvm/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"/snap/bin",
		"/var/lib/snapd/snap/bin",
		join(o, h, ".local", "bin"),
		join(o, h, ".cargo", "bin"),
		join(o, h, ".bun", "bin"),
		join(o, h, ".deno", "bin"),
		join(o, h, ".pyenv", "shims"),
		join(o, h, ".asdf", "shims"),
		join(o, h, ".local", "share", "mise", "shims"),
		join(o, h, ".sdkman", "candidates", "java", "current", "bin"),
	}
}

// originRules are matched in order against a lowercased, slash-separated path.
var originRules = []struct{ substr, origin string }{
	{"msys64", "msys2"},
	{"llvm", "llvm"},
	{"visual studio", "visual-studio"},
	{"/opt/homebrew/", "homebrew"},
	{"/usr/local/cellar/", "homebrew"},
	{"/homebrew/", "homebrew"},
	{"/snap/", "snap"},
	{"snapd/snap", "snap"},
	{".pyenv/shims", "pyenv"},
	{"windowsapps", "WindowsApps"},
}

// ClassifyOrigin names where an executable came from, for display and for
// ordering duplicates. Unknown paths are "system".
func ClassifyOrigin(path, osOverride string) string {
	low := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	for _, r := range originRules {
		if strings.Contains(low, r.substr) {
			return r.origin
		}
	}
	if isWSLPath(low) {
		return "WSL"
	}
	return "system"
}

func isWSLPath(low string) bool {
	if !strings.HasPrefix(low, "/mnt/") || len(low) < 7 {
		return false
	}
	return low[5] >= 'a' && low[5] <= 'z' && low[6] == '/'
}

// ToWSLPath converts a Windows path to its WSL mount equivalent, so a shell
// script can be handed to a POSIX tool.
func ToWSLPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(string(p[0])) + p[2:]
	}
	return p
}
