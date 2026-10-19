// Package platform abstracts the operating system: directory layout, PATH
// scanning, executable resolution and origin classification. Every function
// takes an explicit env and OS name so Windows/macOS logic is testable on Linux.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func home(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := env["USERPROFILE"]; v != "" {
			return v
		}
		if d, p := env["HOMEDRIVE"], env["HOMEPATH"]; d != "" || p != "" {
			return d + p
		}
	}
	return env["HOME"]
}

func appData(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := env["APPDATA"]; v != "" {
			return v
		}
	}
	return home(env, osName)
}

func localAppData(env map[string]string, osName string) string {
	if osName == "windows" {
		if v := env["LOCALAPPDATA"]; v != "" {
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
	for _, p := range strings.Split(env["PATH"], sep) {
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
	raw := env["PATHEXT"]
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
// with any OS executable extension, in name order.
func FindInDir(dir string, names []string, env map[string]string, osOverride string) []string {
	o := OSName(osOverride)
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		for _, ext := range ExeExts(env, o) {
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
