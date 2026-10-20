// Package detect finds every install of a tool on the machine: it builds the
// candidate list, probes each executable for a version, and reports where each
// came from. Probe results are cached by path+mtime+size so a repeat run is
// cheap. A candidate is usable only if launching it with its version arguments
// exits 0 in time — that rejects store stubs and broken installs.
package detect

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/platform"
)

// Install is one discovered executable.
type Install struct {
	Tool    string
	Path    string
	Version string
	Origin  string
	Note    string
	OK      bool
	Pinned  bool
}

var versionRe = regexp.MustCompile(`\d+(\.\d+)+`)

// ExtractVersion pulls the first dotted version out of a tool's banner text.
func ExtractVersion(text string) string {
	return versionRe.FindString(text)
}

type cacheEntry struct {
	Mtime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
	Origin  string `json:"origin"`
	Note    string `json:"note"`
	OK      bool   `json:"ok"`
}

// ProbeCache memoises probe results on disk, keyed by executable path. A corrupt
// file is ignored rather than fatal.
type ProbeCache struct {
	path string
	mu   sync.Mutex
	m    map[string]cacheEntry
	dirs *platform.DirCache
}

// Dirs returns the per-run directory-listing cache, creating it on first use.
// Sharing it across every tool probed in one run is what keeps `doctor` from
// re-listing the same directories once per tool.
//
// The dashboard probes from several goroutines at once — the startup scan, the
// update check, the picker — so the lazy init is guarded. Unguarded, each
// goroutine built its own DirCache: a data race, and a silent loss of exactly
// the sharing this exists to provide.
func (c *ProbeCache) Dirs() *platform.DirCache {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirs == nil {
		c.dirs = platform.NewDirCache()
	}
	return c.dirs
}

// NewProbeCache loads a cache from disk, ignoring any file it cannot parse.
func NewProbeCache(path string) *ProbeCache {
	c := &ProbeCache{path: path, m: map[string]cacheEntry{}}
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var m map[string]cacheEntry
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return c
	}
	c.m = m
	return c
}

// Get returns a cached result when the executable is unchanged.
func (c *ProbeCache) Get(path string, args []string, mtime, size int64) (Install, bool) {
	if c == nil {
		return Install{}, false
	}
	c.mu.Lock()
	e, ok := c.m[probeKey(path, args)]
	c.mu.Unlock()
	if !ok || e.Mtime != mtime || e.Size != size {
		return Install{}, false
	}
	return Install{Path: path, Version: e.Version, Origin: e.Origin, Note: e.Note, OK: e.OK}, true
}

// Put records a probe result.
func (c *ProbeCache) Put(inst Install, args []string, mtime, size int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]cacheEntry{}
	}
	c.m[probeKey(inst.Path, args)] = cacheEntry{Mtime: mtime, Size: size, Version: inst.Version, Origin: inst.Origin, Note: inst.Note, OK: inst.OK}
}

// probeKey identifies a cached verdict. It includes the version arguments
// because the same executable answers differently depending on them: apt-get
// with no arguments exits 1 and looks unusable, while apt-get --version exits 0.
// Keying on the path alone meant that changing a tool's VersionArgs was ignored
// forever on every machine that had already probed it, because the file's mtime
// and size had not changed.
func probeKey(path string, args []string) string {
	if len(args) == 0 {
		return path
	}
	return path + "\x00" + strings.Join(args, "\x00")
}

// Save writes the cache to disk.
func (c *ProbeCache) Save() error {
	if c == nil || c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	c.mu.Lock()
	data, err := json.MarshalIndent(c.m, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

// CandidatePaths lists the executables to try for a tool, in priority order: a
// pinned path, tool-specific dirs, PATH, well-known install dirs, then extraDirs
// from config. Duplicates (same real path) collapse.
func CandidatePaths(t catalog.Tool, cfg config.Config, env map[string]string, osOverride string) []string {
	return CandidatePathsWith(t, cfg, env, osOverride, nil)
}

// CandidatePathsWith is CandidatePaths sharing a directory-listing cache.
func CandidatePathsWith(t catalog.Tool, cfg config.Config, env map[string]string, osOverride string, dc *platform.DirCache) []string {
	var files, dirs []string
	if p := cfg.Paths[t.ID]; p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			files = append(files, p)
		} else {
			dirs = append(dirs, p)
		}
	}
	dirs = append(dirs, toolDirs(t, env, osOverride)...)
	dirs = append(dirs, platform.PathEntries(env, osOverride)...)
	dirs = append(dirs, platform.ExtraSearchDirs(env, osOverride)...)
	dirs = append(dirs, cfg.ExtraDirs...)

	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		key := realPath(p)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, p)
	}
	for _, f := range files {
		if isExec(f, osOverride) {
			add(f)
		}
	}
	for _, d := range dirs {
		for _, p := range platform.FindInDirCached(d, t.Names, env, osOverride, dc) {
			add(p)
		}
	}
	return out
}

func toolDirs(t catalog.Tool, env map[string]string, osOverride string) []string {
	h := platform.Env(env, osOverride, "HOME")
	if h == "" {
		h = platform.Env(env, osOverride, "USERPROFILE")
	}
	var out []string
	for _, d := range t.Dirs {
		if strings.HasPrefix(d, "~/") && h != "" {
			out = append(out, filepath.Join(h, d[2:]))
			continue
		}
		out = append(out, d)
	}
	return out
}

// Probe launches an executable with the tool's version arguments and reads the
// version out of the output. A nonzero exit or a timeout means unusable.
func Probe(t catalog.Tool, path string, cache *ProbeCache, timeout time.Duration) Install {
	mtime, size := statKey(path)
	if inst, ok := cache.Get(path, t.VersionArgs, mtime, size); ok {
		inst.Tool = t.ID
		return inst
	}
	inst := Install{Tool: t.ID, Path: path, Origin: platform.ClassifyOrigin(path, "")}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	argv := append([]string{path}, t.VersionArgs...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	text := string(out)

	timedOut := ctx.Err() == context.DeadlineExceeded
	switch {
	case timedOut:
		inst.Note = "timed out"
	case err != nil:
		inst.Note = "not usable"
	default:
		inst.OK = true
		inst.Version = ExtractVersion(text)
		if t.ID == "gcc" && strings.Contains(strings.ToLower(text), "clang") {
			inst.Note = "is clang"
		}
	}
	if !timedOut {
		cache.Put(inst, t.VersionArgs, mtime, size)
	}
	return inst
}

// FindInstalls probes the candidates for a tool. With validateAll false it stops
// at the first usable install; low-priority origins (WSL, WindowsApps) sort last
// so a shim never shadows a real toolchain.
func FindInstalls(t catalog.Tool, cfg config.Config, cache *ProbeCache, validateAll bool, env map[string]string, osOverride string) []Install {
	if t.Special == "msvc" {
		return FindMSVC()
	}
	var out []Install
	dc := cache.Dirs()
	for _, p := range CandidatePathsWith(t, cfg, env, osOverride, dc) {
		inst := Probe(t, p, cache, 5*time.Second)
		if cfg.Paths[t.ID] == p {
			inst.Pinned = true
		}
		out = append(out, inst)
		if inst.OK && !validateAll {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		a, b := platform.LowPriority[out[i].Origin], platform.LowPriority[out[j].Origin]
		if a != b {
			return !a
		}
		return false
	})
	return out
}

// FindMSVC locates MSVC through vswhere. cl.exe needs vcvars64.bat, which the
// planner arranges; on any other OS this returns nothing.
func FindMSVC() []Install {
	if platform.OSName("") != "windows" {
		return nil
	}
	root := os.Getenv("ProgramFiles(x86)")
	if root == "" {
		return nil
	}
	vswhere := filepath.Join(root, "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if _, err := os.Stat(vswhere); err != nil {
		return nil
	}
	out, err := exec.Command(vswhere, "-latest", "-products", "*",
		"-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"-property", "installationPath").Output()
	if err != nil {
		return nil
	}
	installPath := strings.TrimSpace(string(out))
	if installPath == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(installPath, "VC", "Tools", "MSVC", "*", "bin", "Hostx64", "x64", "cl.exe"))
	var installs []Install
	for _, m := range matches {
		installs = append(installs, Install{Tool: "msvc", Path: m, Origin: "visual-studio", OK: true, Note: "needs vcvars64"})
	}
	return installs
}

func statKey(path string) (int64, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return fi.ModTime().UnixNano(), fi.Size()
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func isExec(p, osOverride string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if platform.OSName(osOverride) == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}
