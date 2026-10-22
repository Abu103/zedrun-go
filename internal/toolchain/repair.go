package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/provision"
)

// Kind classifies what is wrong. It is the row of the decision table that the
// symptoms matched, and it is stable so an assistant can switch on it.
type Kind string

const (
	KindOK             Kind = "ok"
	KindNotInstalled   Kind = "not-installed"   // nothing usable, a route can install it
	KindManagerMissing Kind = "manager-missing" // nothing usable, no route's manager is installed
	KindNoRoute        Kind = "no-route"        // nothing usable and no managed route on this OS
	KindNeedsAdmin     Kind = "needs-admin"     // Windows route needs an elevated terminal
	KindNoSudo         Kind = "no-sudo"         // POSIX route needs root and sudo -n fails
	KindNotOnPath      Kind = "not-on-path"     // usable install exists, its directory is not on PATH
	KindStalePath      Kind = "stale-path"      // the profile already has the directory; this shell predates it
	KindShimmed        Kind = "shimmed"         // a shim wins PATH order over the real executable
)

// Shape is which of the three instruction forms a fix takes.
type Shape string

const (
	ShapeNone  Shape = ""
	ShapeTool  Shape = "a" // zedrun-go does it: `zedrun-go toolchain repair <tool>`
	ShapePath  Shape = "b" // a PATH line the user appends to their own profile
	ShapeBlock Shape = "c" // a plain copy-paste block
)

// Diagnosis is the classified cause for one tool.
type Diagnosis struct {
	Tool    string       `json:"tool"`
	Kind    Kind         `json:"kind"`
	Symptom string       `json:"symptom"`
	Cause   string       `json:"cause"`
	Dir     string       `json:"dir,omitempty"` // the directory a PATH fix must surface
	Install *InstallInfo `json:"install,omitempty"`
}

// Step is one instruction.
type Step struct {
	Title   string `json:"title"`
	Shell   Shell  `json:"shell,omitempty"`
	File    string `json:"file,omitempty"` // profile the command belongs in, when it does
	Command string `json:"command"`        // exact text; paste as-is
	// Explain marks a step zedrun-go shows but never runs: it needs a password
	// or an elevated terminal that only the user can supply.
	Explain  bool   `json:"explain,omitempty"`
	Elevated bool   `json:"elevated,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Plan is the answer to "what do I do now".
type Plan struct {
	Tool    string            `json:"tool"`
	Kind    Kind              `json:"kind"`
	Shape   Shape             `json:"shape"`
	Summary string            `json:"summary"`
	Cause   string            `json:"cause,omitempty"`
	Steps   []Step            `json:"steps,omitempty"`
	Verify  []string          `json:"verify,omitempty"`
	Confirm string            `json:"confirm,omitempty"` // ask before continuing
	Stop    string            `json:"stop,omitempty"`    // the plan cannot continue without the user
	Route   *provision.Method `json:"route,omitempty"`
	// CanRun is true when zedrun-go itself may execute Route now: the manager is
	// present and any needed elevation is available without a password.
	CanRun bool `json:"can_run"`
	// Lands is where a direct download deposits its file.
	Lands string `json:"lands,omitempty"`
}

// Options tunes Repair. The zero value reads the real file system.
type Options struct {
	Shell    Shell
	CacheDir string                       // where direct downloads land
	ReadFile func(string) ([]byte, error) // injectable profile reader
}

func (o Options) read(p string) ([]byte, error) {
	if o.ReadFile != nil {
		return o.ReadFile(p)
	}
	return os.ReadFile(p)
}

// Diagnose classifies a Status into one Kind.
func Diagnose(st Status, o Options) Diagnosis {
	d := Diagnosis{Tool: st.Tool}
	switch {
	case st.Present && st.Active != nil:
		if st.Active.Shim {
			if real, ok := realInstall(st); ok {
				d.Kind, d.Dir, d.Install = KindShimmed, real.Dir, &real
				d.Symptom = fmt.Sprintf("`%s` runs the launcher at %s", exeName(st.Tool), st.Active.Path)
				d.Cause = fmt.Sprintf("a shim earlier in PATH hides the real executable at %s", real.Path)
				return d
			}
		}
		d.Kind = KindOK
		d.Install = st.Active
		d.Symptom = fmt.Sprintf("%s %s runs from %s", st.Tool, orUnknown(st.Active.Version), st.Active.Path)
		return d

	case st.Present: // usable, but no install directory is on PATH
		pick := st.OffPath[0]
		for _, c := range st.OffPath {
			if !c.Shim {
				pick = c
				break
			}
		}
		d.Dir, d.Install = pick.Dir, &pick
		d.Symptom = fmt.Sprintf("`%s` is not found in this shell", exeName(st.Tool))
		if o.profileHas(st, pick.Dir) {
			d.Kind = KindStalePath
			d.Cause = "the directory is already in your shell profile; this window started before it was added"
			return d
		}
		d.Kind = KindNotOnPath
		d.Cause = fmt.Sprintf("%s is installed at %s but that directory is not on PATH", st.Tool, pick.Path)
		return d
	}

	d.Symptom = fmt.Sprintf("`%s` is not installed", exeName(st.Tool))
	switch {
	case st.Route == nil && st.ManagerMissing:
		d.Kind = KindManagerMissing
		d.Cause = "none of the package managers that can install it are present on this machine"
	case st.Route == nil:
		d.Kind = KindNoRoute
		d.Cause = "zedrun-go has no package-manager route for this tool on " + st.OS
	case st.Elevation.Needed && !st.Elevation.CanRun && st.OS == "windows":
		d.Kind = KindNeedsAdmin
		d.Cause = "the install needs an Administrator terminal and zedrun-go will not elevate on its own"
	case st.Elevation.Needed && !st.Elevation.CanRun:
		d.Kind = KindNoSudo
		d.Cause = "the install needs root and this shell has no passwordless sudo"
	default:
		d.Kind = KindNotInstalled
		d.Cause = fmt.Sprintf("%s is not installed and %s can install it", st.Tool, st.Route.Manager)
	}
	return d
}

// realInstall finds a usable non-shim install, preferring one on PATH.
func realInstall(st Status) (InstallInfo, bool) {
	var off *InstallInfo
	for _, i := range st.Installs {
		if !i.OK || i.Shim {
			continue
		}
		if i.OnPath {
			return i, true
		}
		if off == nil {
			c := i
			off = &c
		}
	}
	if off != nil {
		return *off, true
	}
	return InstallInfo{}, false
}

// profileHas reports whether the user's profile for the active shell already
// mentions dir. Only POSIX profiles are read: Windows PATH lives in the
// registry, which a plain file read cannot see.
func (o Options) profileHas(st Status, dir string) bool {
	if st.OS == "windows" {
		return false
	}
	shell := o.Shell
	if shell == ShellUnknown {
		shell = st.Shell
	}
	if shell == ShellUnknown {
		shell = ShellBash
	}
	home := st.Env["HOME"]
	if home == "" {
		return false
	}
	pl := PathAppend(dir, shell, st.OS)
	files := []string{pl.File}
	if shell == ShellBash {
		files = append(files, "~/.bash_profile", "~/.profile")
	}
	for _, f := range files {
		if f == "" {
			continue
		}
		b, err := o.read(filepath.Join(home, strings.TrimPrefix(f, "~/")))
		if err == nil && strings.Contains(string(b), dir) {
			return true
		}
	}
	return false
}

// Repair maps a diagnosis to exact instructions in one of the three shapes.
func Repair(st Status, d Diagnosis, o Options) Plan {
	p := Plan{Tool: st.Tool, Kind: d.Kind, Cause: d.Cause, Verify: verifyCommands(st, o)}
	shell := o.Shell
	if shell == ShellUnknown {
		shell = st.Shell
	}

	switch d.Kind {
	case KindOK:
		p.Summary = d.Symptom
		return p

	case KindNotOnPath, KindShimmed:
		p.Shape = ShapePath
		if d.Kind == KindShimmed {
			p.Summary = fmt.Sprintf("put %s ahead of the shim in PATH so `%s` runs the real executable", d.Dir, exeName(st.Tool))
		} else {
			p.Summary = fmt.Sprintf("add %s to PATH so `%s` is found", d.Dir, exeName(st.Tool))
		}
		shells := []Shell{shell}
		if shell == ShellUnknown {
			shells = ShellsFor(st.OS)
		}
		for _, sh := range shells {
			pl := PathAppend(d.Dir, sh, st.OS)
			p.Steps = append(p.Steps, pathSteps(pl, d.Dir)...)
		}
		p.Stop = "run these in the same shell you start Zed from, then open a NEW window and verify there"
		return p

	case KindStalePath:
		p.Shape = ShapePath
		p.Summary = fmt.Sprintf("%s is already in your profile; reload it, or open a new terminal", d.Dir)
		sh := shell
		if sh == ShellUnknown {
			sh = ShellBash
		}
		pl := PathAppend(d.Dir, sh, st.OS)
		p.Steps = []Step{{Title: "reload your profile in this shell", Shell: sh, Command: pl.Reload}}
		return p

	case KindManagerMissing:
		p.Shape = ShapeBlock
		p.Summary = "install a package manager first, then re-run the repair"
		for _, m := range st.Routes {
			p.Steps = append(p.Steps, managerBootstrap(m.Manager, st.OS))
			break // the first route is the preferred manager; one is enough
		}
		p.Steps = append(p.Steps, Step{
			Title:   "then let zedrun-go install the tool",
			Command: "zedrun-go toolchain repair " + st.Tool,
		})
		return p

	case KindNoRoute:
		p.Shape = ShapeBlock
		p.Summary = "install it by hand"
		p.Steps = []Step{{Title: "manual install", Command: catalog.InstallHint(st.Tool, st.OS)}}
		return p

	case KindNeedsAdmin, KindNoSudo:
		p.Shape = ShapeTool
		p.Route = st.Route
		p.Summary = fmt.Sprintf("%s needs administrator rights; run the command yourself", st.Route.Manager)
		cmd := st.Route.Command()
		note := "open Terminal as Administrator, paste the command, then paste its output back"
		if d.Kind == KindNoSudo {
			cmd = "sudo " + cmd
			note = "sudo will ask for your password; type it yourself, zedrun-go never sees it"
			if _, ok := provision.LookPath("sudo", st.Env); !ok {
				cmd = st.Route.Command()
				note = "sudo is not installed here: become root first (`su -`), then run this without sudo"
			}
		}
		p.Steps = []Step{{Title: "run as administrator", Command: cmd, Explain: true, Elevated: true, Note: note}}
		p.Stop = "zedrun-go will not ask for or accept a password; stop here until the command has been run"
		return p
	}

	// KindNotInstalled: the tool can do it.
	p.Shape = ShapeTool
	p.Route = st.Route
	p.CanRun = true
	p.Confirm = fmt.Sprintf("%s will install the package for %s; the manager chooses the version. Continue?", st.Route.Manager, st.Tool)
	step := Step{
		Title:   fmt.Sprintf("install %s with %s", st.Tool, st.Route.Manager),
		Command: "zedrun-go toolchain repair " + st.Tool + " --run --yes",
		Note:    "runs: " + st.Route.Command(),
	}
	if st.Route.Downloadable() {
		p.Lands = filepath.Join(o.CacheDir, "downloads", st.Tool)
		step.Note = fmt.Sprintf("downloads %s to %s with a progress bar; then run: %s", st.Route.URL, p.Lands, st.Route.Note)
	}
	p.Steps = []Step{step}
	return p
}

// pathSteps expands one PathLine into the steps a user pastes: the persistent
// change, the same change for this window, and how to reload.
func pathSteps(pl PathLine, dir string) []Step {
	var steps []Step
	title := fmt.Sprintf("add %s to PATH permanently (%s)", dir, pl.Shell)
	if pl.File != "" {
		title = fmt.Sprintf("append this line to %s (%s)", pl.File, pl.Shell)
	}
	steps = append(steps, Step{Title: title, Shell: pl.Shell, File: pl.File, Command: pl.Line, Note: pl.Note})
	if pl.Session != "" && pl.Session != pl.Line {
		steps = append(steps, Step{Title: "or only for the current window (" + string(pl.Shell) + ")", Shell: pl.Shell, Command: pl.Session})
	}
	if pl.Reload != "" {
		steps = append(steps, Step{Title: "pick it up (" + string(pl.Shell) + ")", Shell: pl.Shell, Command: pl.Reload})
	}
	return steps
}

func managerBootstrap(manager, osName string) Step {
	switch manager {
	case "winget":
		return Step{Title: "install winget (App Installer)", Command: "https://aka.ms/getwinget",
			Note: "open that link, install App Installer, then open a new terminal"}
	case "brew":
		return Step{Title: "install Homebrew", Command: `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`}
	case "scoop":
		return Step{Title: "install Scoop (PowerShell)", Command: "irm get.scoop.sh | iex"}
	}
	return Step{
		Title:   "install " + manager,
		Command: "install " + manager + " for " + osName + ", then open a new terminal",
		Note:    "zedrun-go has no bootstrap command for " + manager,
	}
}

// verifyCommands are the probes that prove the fix, run in a NEW terminal.
func verifyCommands(st Status, o Options) []string {
	var v []string
	t, ok := catalog.Tools[st.Tool]
	if ok && len(t.Names) > 0 {
		v = append(v, strings.TrimSpace(t.Names[0]+" "+strings.Join(t.VersionArgs, " ")))
	}
	return append(v, "zedrun-go toolchain verify "+st.Tool)
}

func exeName(tool string) string {
	if t, ok := catalog.Tools[tool]; ok && len(t.Names) > 0 {
		return t.Names[0]
	}
	return tool
}

func orUnknown(s string) string {
	if s == "" {
		return "(version unknown)"
	}
	return s
}
