package provision

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Method is one way to obtain a toolchain.
type Method struct {
	Tool     string   // catalog tool id
	Manager  string   // manager name, or "direct"
	Package  string   // package name; empty for direct
	Argv     []string // ready-to-run argv; empty for direct
	URL      string   // for direct: where to get it
	Elevated bool     // needs administrator or root
	Note     string   // one line shown beside the command
	Present  bool     // the manager is installed on this machine
}

// Runnable reports whether zedrun-go can execute this method itself. A direct
// method has no argv, so it can only be described.
func (m Method) Runnable() bool { return len(m.Argv) > 0 }

// Command is the exact command, or the URL when there is nothing to run.
func (m Method) Command() string {
	if len(m.Argv) > 0 {
		return strings.Join(m.Argv, " ")
	}
	return m.URL
}

// LookPath finds an executable in the PATH carried by env, falling back to the
// process PATH when env has none.
//
// exec.Command resolves its executable at construction time using the parent's
// PATH and ignores cmd.Env, so a manager detected in a non-standard directory
// would be passed to a child that cannot find it. Resolving here keeps the
// detection environment and the execution environment the same.
func LookPath(name string, env map[string]string) (string, bool) {
	if strings.ContainsRune(name, os.PathSeparator) {
		if _, err := os.Stat(name); err == nil {
			return name, true
		}
		return "", false
	}
	search := env["PATH"]
	if search == "" {
		p, err := exec.LookPath(name)
		return p, err == nil
	}
	for _, dir := range filepath.SplitList(search) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		// On Windows the name may need an extension; the table uses bare names
		// there, so try the standard ones before giving up.
		if fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// writerOr returns w, or a sink when w is nil. A caller that has nowhere to
// stream — a library user, a test — must not have to invent a buffer just to
// keep Run from panicking on a nil interface.
func writerOr(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// envSlice flattens an environment map into the KEY=VALUE form exec wants. The
// order is the map's, which is fine: exec does not care, and Go's own
// os.Environ is already unordered.
func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// substitute replaces {package} in an argv template. The result is argv, never a
// shell string, so a package name containing a space or a semicolon is passed
// through as one argument instead of being interpreted.
func substitute(tmpl []string, pkg string) []string {
	out := make([]string, len(tmpl))
	for i, a := range tmpl {
		out[i] = strings.ReplaceAll(a, "{package}", pkg)
	}
	return out
}

// Available lists every way to get a tool on this OS, best first.
//
// A manager that is not installed is left out unless all is set, in which case
// it is included with Present false so the user can see the whole menu and
// install the manager first. A direct download is always last: it is the route
// that leaves the tool unmanaged, so it should not be the default.
func Available(toolID, osName string, detected map[string]string, all bool) []Method {
	// A windowsOnly tool is not merely "not packaged on Linux": it does not
	// exist there, and a direct download link is not a route to it.
	if osName == "linux" && windowsOnly[toolID] {
		return nil
	}
	pkgs := packages[toolID]
	var out []Method
	for _, m := range managerByOS[osName] {
		pkg, ok := pkgs[m.Name]
		if !ok {
			continue
		}
		_, present := detected[m.Name]
		if !present && !all {
			continue
		}
		out = append(out, Method{
			Tool: toolID, Manager: m.Name, Package: pkg,
			Argv: substitute(m.Install, pkg), Elevated: m.Elevated, Present: present,
		})
	}
	if d, ok := directURL[toolID]; ok {
		out = append(out, Method{
			Tool: toolID, Manager: "direct", URL: d.URL, Note: d.Note, Present: true,
		})
	}
	return out
}

// UpdateArgs is the argv that updates an already-installed tool through the
// manager that owns it. An empty result means the manager has no update route,
// which is honest: saying nothing is better than running an install again.
func (m Manager) UpdateArgs(pkg string) []string {
	if len(m.Update) == 0 {
		return nil
	}
	return substitute(m.Update, pkg)
}

// Update returns the argv that updates an already-installed tool through this
// method's manager. The second result is false when that manager has no update
// route — saying so is better than silently running an install again.
func (m Method) Update() ([]string, bool) {
	mgr, ok := managerNamed(m.Manager)
	if !ok || m.Package == "" {
		return nil, false
	}
	argv := mgr.UpdateArgs(m.Package)
	return argv, len(argv) > 0
}

// Order moves the method owned by the manager that already installed this tool
// to the front, so that updating in place is what a bare `get --run` does.
// owner is an origin name from platform.ClassifyOrigin, or "" when unknown.
func Order(ms []Method, owner string) []Method {
	if owner == "" {
		return ms
	}
	out := make([]Method, len(ms))
	copy(out, ms)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Manager == owner && out[j].Manager != owner
	})
	return out
}

// Render writes one line per method: the exact command, and what it needs.
func Render(w io.Writer, toolID string, ms []Method) {
	if len(ms) == 0 {
		fmt.Fprintf(w, "no way to install %s is known on this platform\n", toolID)
		fmt.Fprintf(w, "  the tool may still be installed by hand; run `zedrun-go doctor` to see what was found\n")
		return
	}
	fmt.Fprintf(w, "%d way(s) to get %s on this machine:\n\n", len(ms), toolID)
	for i, m := range ms {
		mark := " "
		if i == 0 && m.Runnable() {
			mark = "*"
		}
		fmt.Fprintf(w, "%s %-8s %s\n", mark, m.Manager, m.Command())
		var notes []string
		if !m.Present {
			notes = append(notes, "manager not installed")
		}
		if m.Elevated {
			notes = append(notes, "needs administrator or root")
		}
		if m.Note != "" {
			notes = append(notes, m.Note)
		}
		if len(notes) > 0 {
			fmt.Fprintf(w, "             %s\n", strings.Join(notes, " · "))
		}
	}
	fmt.Fprintf(w, "\n* = what `--run` would use\n")
}

// RunOptions controls how a method is executed.
type RunOptions struct {
	// Elevate is the resolved elevation decision from the caller: argv already
	// carries sudo when it was safe to add it. Run never invents elevation on
	// its own, because a tool like this must not start asking for a password
	// it cannot supply.
	Elevate bool
	// Timeout kills the installer after this long. Zero means wait forever.
	// A package manager that is waiting on a network that is not there must
	// not hold the terminal open indefinitely, and the default is deliberately
	// generous because a real install can be slow.
	Timeout time.Duration
	// Env is the environment for the installer. Nil means inherit the parent's.
	// Passing the environment detection used is not cosmetic: a manager found
	// on a non-standard PATH entry is invisible to the child otherwise, and
	// the run fails with "executable file not found" for a manager that was
	// just detected.
	Env map[string]string
	// Stdout and Stderr receive the installer's own output.
	Stdout, Stderr io.Writer
	// IsRoot reports that the process already has root, so no sudo is needed.
	IsRoot bool
	// OS is the target platform. Windows has no sudo, so an elevated method
	// there is explained instead of prefixed.
	OS string
}

// Elevate decides how to run an elevated method.
//
// Prepending sudo whenever the caller feels like it is the wrong rule: sudo
// reads the password from the terminal, and a non-interactive run has no
// terminal, so it dies with "a terminal is required" — the run never reaches
// the manager at all. The rule:
//
//   - root, or a method that does not need elevation, runs as given;
//   - a passwordless sudo (probed in the same environment the tool was
//     detected with) runs through sudo, because that is the one case where the
//     tool can genuinely finish the job;
//   - anything else prints the exact command for the user to run themselves.
//
// The second result is false when the method cannot be run here.
func Elevate(m Method, osName string, isRoot bool, env map[string]string) (argv []string, ok bool, note string) {
	if !m.Elevated || isRoot {
		return m.Argv, true, ""
	}
	if osName == "windows" {
		return nil, false, "this needs administrator rights. open a terminal as Administrator and run:\n  " + m.Command()
	}
	if PasswordlessSudo(env) {
		return append([]string{"sudo"}, m.Argv...), true, ""
	}
	return nil, false, "this needs administrator rights and this shell has no passwordless sudo.\nrun it yourself:\n  sudo " + m.Command()
}

// SudoProbe is the passwordless-sudo check, replaceable in tests.
var SudoProbe = func(env map[string]string) bool {
	path, ok := LookPath("sudo", env)
	if !ok {
		return false
	}
	cmd := exec.Command(path, "-n", "true")
	if len(env) > 0 {
		cmd.Env = envSlice(env)
	}
	return cmd.Run() == nil
}

// PasswordlessSudo reports whether sudo can run without prompting.
func PasswordlessSudo(env map[string]string) bool { return SudoProbe(env) }

// Run executes a method and returns its exit code, or -1 when it could not be
// started at all or was killed for exceeding the timeout. The argv comes from
// the table, so there is no shell involved.
func Run(m Method, o RunOptions) int {
	if !m.Runnable() {
		fmt.Fprintf(o.Stderr, "%s has no command to run; get it from %s\n", m.Tool, m.URL)
		return -1
	}
	argv := m.Argv
	if m.Elevated && !o.IsRoot && !o.Elevate {
		fmt.Fprintf(o.Stderr,
			"this needs administrator rights and zedrun-go will not elevate on its own.\nrun it yourself:\n  sudo %s\n",
			m.Command())
		return -1
	}
	fmt.Fprintf(writerOr(o.Stdout), "$ %s\n", strings.Join(argv, " "))

	ctx := context.Background()
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	run := argv
	if resolved, ok := LookPath(argv[0], o.Env); ok {
		run = append([]string{resolved}, argv[1:]...)
	}
	cmd := exec.CommandContext(ctx, run[0], run[1:]...)
	if len(o.Env) > 0 {
		cmd.Env = envSlice(o.Env)
	}
	cmd.Stdout = writerOr(o.Stdout)
	cmd.Stderr = writerOr(o.Stderr)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(o.Stderr, "%s timed out after %s and was stopped\n", argv[0], o.Timeout)
			return -1
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(o.Stderr, "could not run %s: %v\n", argv[0], err)
		return -1
	}
	return 0
}

// OwnerFor maps an install origin to the manager most likely to own it, so that
// a bare `get --run` updates the copy already on the machine instead of adding a
// second one beside it. An origin it does not recognise returns "", and the
// caller then leaves the order alone rather than guessing.
func OwnerFor(origin string) string {
	switch origin {
	case "rustup":
		return "rustup"
	case "homebrew":
		return "brew"
	case "snap":
		return "snap"
	case "msys2":
		return "winget"
	case "pyenv":
		return "pip"
	case "WindowsApps":
		return "winget"
	}
	return ""
}
