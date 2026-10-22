// Package toolchain is zedrun-go's toolchain-assist subsystem: one call that
// diagnoses why a language toolchain is not usable (Status), a deterministic
// mapping from that diagnosis to exact instructions (Repair), and a small
// on-disk record (Resume) that lets a second person, or a second sandboxed
// assistant, continue a half-finished setup without re-explaining anything.
//
// Nothing here runs a shell string. Commands that zedrun-go can run itself are
// carried as argv; everything else is text for a human to copy, and the
// package never writes to a shell profile or elevates on its own.
package toolchain

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/provision"
)

// InstallInfo is one discovered executable plus where it sits relative to PATH.
type InstallInfo struct {
	Path      string `json:"path"`
	Dir       string `json:"dir"`
	Version   string `json:"version,omitempty"`
	Origin    string `json:"origin,omitempty"`
	OK        bool   `json:"ok"`
	Pinned    bool   `json:"pinned,omitempty"`
	OnPath    bool   `json:"on_path"`
	PathIndex int    `json:"path_index"` // position of Dir in PATH, -1 when absent
	Shim      bool   `json:"shim,omitempty"`
}

// Shadow records one install hidden behind another one earlier in PATH.
type Shadow struct {
	Winner InstallInfo `json:"winner"`
	Loser  InstallInfo `json:"loser"`
}

// Elevation describes whether a route that needs administrator rights can be
// run by zedrun-go at all on this machine.
type Elevation struct {
	Needed bool   `json:"needed"`
	Root   bool   `json:"root"`
	Sudo   bool   `json:"sudo"` // passwordless sudo probed with `sudo -n`
	CanRun bool   `json:"can_run"`
	Note   string `json:"note,omitempty"`
}

// Status is the full diagnosis input for one tool.
type Status struct {
	Tool     string        `json:"tool"`
	OS       string        `json:"os"`
	Shell    Shell         `json:"shell,omitempty"`
	Present  bool          `json:"present"` // at least one usable install exists
	Origin   string        `json:"origin,omitempty"`
	Pinned   string        `json:"pinned,omitempty"`
	WSL      bool          `json:"wsl,omitempty"`
	Installs []InstallInfo `json:"installs,omitempty"`
	// Active is what a bare command resolves to: the usable install whose
	// directory comes first in PATH. Nil when nothing usable is on PATH.
	Active   *InstallInfo  `json:"active,omitempty"`
	OffPath  []InstallInfo `json:"off_path,omitempty"` // usable but not on PATH
	Shadowed []Shadow      `json:"shadowed,omitempty"`

	Managers       map[string]string  `json:"managers,omitempty"`
	Routes         []provision.Method `json:"routes,omitempty"`
	Route          *provision.Method  `json:"route,omitempty"` // best route that can run now
	ManagerMissing bool               `json:"manager_missing,omitempty"`
	Elevation      Elevation          `json:"elevation"`

	// Env and IsRoot are what the diagnosis was made with; Repair reuses them
	// so the elevation decision is made in the same environment.
	Env    map[string]string `json:"-"`
	IsRoot bool              `json:"-"`
}

// Inputs is everything Assemble needs. Splitting it from Detect keeps the
// decision logic pure, so tests drive it without touching the file system.
type Inputs struct {
	Tool     string
	OS       string
	Shell    Shell
	Env      map[string]string
	Installs []detect.Install
	Detected map[string]string
	Routes   []provision.Method
	IsRoot   bool
	WSL      bool
}

// Detect runs one full diagnosis for a tool against the real machine.
func Detect(ctx context.Context, cfg config.Config, cache *detect.ProbeCache, env map[string]string, osName, tool string, isRoot bool) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	t, ok := catalog.Tools[tool]
	if !ok {
		return Status{}, &UnknownToolError{Tool: tool}
	}
	installs := detect.FindInstalls(t, cfg, cache, true, env, osName)
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	detected := provision.Detected(cfg, cache, env, osName)
	routes := provision.Available(tool, osName, detected, true)
	origin := ""
	for _, in := range installs {
		if in.OK {
			origin = in.Origin
			break
		}
	}
	routes = provision.Order(routes, provision.OwnerFor(origin))
	return Assemble(Inputs{
		Tool:     tool,
		OS:       osName,
		Shell:    DetectShell(env, osName),
		Env:      env,
		Installs: installs,
		Detected: detected,
		Routes:   routes,
		IsRoot:   isRoot,
		WSL:      platform.IsWSL(env, osName),
	}), nil
}

// UnknownToolError reports a tool id that is not in the catalog.
type UnknownToolError struct{ Tool string }

func (e *UnknownToolError) Error() string { return "unknown tool " + quoteIfNeeded(e.Tool) }

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

// Assemble turns raw detection output into a Status. It is pure apart from the
// passwordless-sudo probe, which is only consulted when the best route needs
// elevation on a POSIX system.
func Assemble(in Inputs) Status {
	st := Status{
		Tool:     in.Tool,
		OS:       in.OS,
		Shell:    in.Shell,
		WSL:      in.WSL,
		Managers: in.Detected,
		Routes:   in.Routes,
		Env:      in.Env,
		IsRoot:   in.IsRoot,
	}
	pathDirs := pathIndex(in.Env, in.OS)

	for _, d := range in.Installs {
		info := InstallInfo{
			Path:      d.Path,
			Dir:       DirOf(d.Path, in.OS),
			Version:   d.Version,
			Origin:    d.Origin,
			OK:        d.OK,
			Pinned:    d.Pinned,
			PathIndex: -1,
			Shim:      IsShim(d.Path, in.OS),
		}
		if i, ok := pathDirs[normDir(info.Dir, in.OS)]; ok {
			info.PathIndex = i
			info.OnPath = true
		}
		if d.Pinned {
			st.Pinned = d.Path
		}
		st.Installs = append(st.Installs, info)
	}

	// Usable installs ordered the way a shell resolves them: PATH position
	// first, off-PATH installs last. The sort is stable so detection's own
	// priority order breaks ties.
	var usable []InstallInfo
	for _, i := range st.Installs {
		if i.OK {
			usable = append(usable, i)
		}
	}
	sort.SliceStable(usable, func(a, b int) bool {
		ia, ib := usable[a].PathIndex, usable[b].PathIndex
		switch {
		case ia < 0 && ib < 0:
			return false
		case ia < 0:
			return false
		case ib < 0:
			return true
		}
		return ia < ib
	})
	st.Present = len(usable) > 0
	if st.Present {
		st.Origin = usable[0].Origin
	}
	for i := range usable {
		if usable[i].OnPath {
			a := usable[i]
			st.Active = &a
			break
		}
	}
	for _, u := range usable {
		if !u.OnPath {
			st.OffPath = append(st.OffPath, u)
		}
	}
	if st.Active != nil {
		for _, u := range usable {
			if u.OnPath && u.Path != st.Active.Path {
				st.Shadowed = append(st.Shadowed, Shadow{Winner: *st.Active, Loser: u})
			}
		}
	}

	// Best route that can actually run now: its manager is installed and it is
	// either runnable (argv) or a direct download zedrun can fetch itself.
	runnableRoute := -1
	anyPresent := false
	for i, m := range st.Routes {
		if m.Present || m.Downloadable() {
			anyPresent = true
		}
		if runnableRoute < 0 && (m.Present && m.Runnable() || m.Downloadable()) {
			runnableRoute = i
		}
	}
	if runnableRoute >= 0 {
		m := st.Routes[runnableRoute]
		st.Route = &m
	}
	// "manager missing" means routes exist for this OS but none can run yet.
	st.ManagerMissing = !anyPresent && len(st.Routes) > 0

	if st.Route != nil && st.Route.Elevated {
		st.Elevation.Needed = true
		st.Elevation.Root = in.IsRoot
		if !in.IsRoot && in.OS != "windows" {
			st.Elevation.Sudo = provision.PasswordlessSudo(in.Env)
		}
		_, ok, note := provision.Elevate(*st.Route, in.OS, in.IsRoot, in.Env)
		st.Elevation.CanRun = ok
		st.Elevation.Note = note
	}
	return st
}

// pathIndex maps each normalised PATH directory to its first position.
func pathIndex(env map[string]string, osName string) map[string]int {
	idx := map[string]int{}
	for i, d := range platform.PathEntries(env, osName) {
		k := normDir(d, osName)
		if _, seen := idx[k]; !seen {
			idx[k] = i
		}
	}
	return idx
}

// DirOf returns the directory part of a path using the separators of the
// target OS, not the host's, so a Windows path is split correctly in a test
// that runs on Linux.
func DirOf(p, osName string) string {
	if osName == "windows" {
		if i := strings.LastIndexAny(p, `\/`); i >= 0 {
			if i == 2 && len(p) > 2 && p[1] == ':' { // C:\x -> C:\
				return p[:3]
			}
			return p[:i]
		}
		return "."
	}
	return filepath.ToSlash(filepath.Dir(p))
}

// normDir folds a directory for comparison: Windows paths are case-insensitive
// and accept either separator, and a trailing separator never matters.
func normDir(d, osName string) string {
	if osName == "windows" {
		d = strings.ReplaceAll(d, "/", `\`)
		d = strings.TrimRight(d, `\`)
		return strings.ToLower(d)
	}
	if len(d) > 1 {
		d = strings.TrimRight(d, "/")
	}
	return d
}

// IsShim reports whether a path is a launcher stub rather than the real
// executable: a batch wrapper, or something living in a known shim directory.
// A shim that wins PATH order hides the tool the user thinks they installed.
func IsShim(p, osName string) bool {
	l := strings.ToLower(p)
	if osName == "windows" {
		if strings.HasSuffix(l, ".bat") || strings.HasSuffix(l, ".cmd") {
			return true
		}
		for _, frag := range []string{`\shims\`, `\windowsapps\`, `\chocolatey\bin\`} {
			if strings.Contains(strings.ReplaceAll(l, "/", `\`), frag) {
				return true
			}
		}
		return false
	}
	return strings.Contains(l, "/shims/")
}
