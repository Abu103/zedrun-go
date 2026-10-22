package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/provision"
	"github.com/zedrun/zedrun-go/internal/toolchain"
)

var toolchainSubcommands = []string{"status", "diagnose", "repair", "verify", "how-to", "set-path", "resume"}

// tcFlags is the union of flags the toolchain subcommands accept. One parser
// keeps `--json` and `--shell` meaning the same thing everywhere.
type tcFlags struct {
	tool    string
	json    bool
	shell   toolchain.Shell
	run     bool
	yes     bool
	using   string
	timeout time.Duration
	project string
	noState bool
}

func parseTCFlags(sub string, args []string, errOut io.Writer) (tcFlags, int) {
	var f tcFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(name string) (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(errOut, "%s needs a value\n", name)
				return "", false
			}
			i++
			return args[i], true
		}
		switch a {
		case "--json":
			f.json = true
		case "--run":
			f.run = true
		case "--yes", "-y":
			f.yes = true
		case "--no-state":
			f.noState = true
		case "--shell":
			v, ok := need(a)
			if !ok {
				return f, 2
			}
			sh, valid := toolchain.ParseShell(v)
			if !valid {
				fmt.Fprintf(errOut, "unknown shell %q: use bash, zsh, fish, sh, powershell, pwsh or cmd\n", v)
				return f, 2
			}
			f.shell = sh
		case "--using":
			v, ok := need(a)
			if !ok {
				return f, 2
			}
			f.using = v
		case "--project":
			v, ok := need(a)
			if !ok {
				return f, 2
			}
			f.project = v
		case "--timeout":
			v, ok := need(a)
			if !ok {
				return f, 2
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				fmt.Fprintf(errOut, "bad --timeout %q\n", v)
				return f, 2
			}
			f.timeout = d
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(errOut, "unknown flag %s\n", a)
				return f, 2
			}
			if f.tool != "" {
				fmt.Fprintf(errOut, "toolchain %s takes one argument, got %q and %q\n", sub, f.tool, a)
				return f, 2
			}
			f.tool = a
		}
	}
	return f, 0
}

func cmdToolchain(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	opts.Cache.ResetDirs() // one command is one run: never reuse a stale listing
	if len(args) == 0 {
		fmt.Fprint(errOut, commandHelp["toolchain"])
		return 2
	}
	sub, rest := args[0], args[1:]
	if sub == "-h" || sub == "--help" || sub == "help" {
		fmt.Fprint(out, commandHelp["toolchain"])
		return 0
	}
	known := false
	for _, s := range toolchainSubcommands {
		if s == sub {
			known = true
		}
	}
	if !known {
		fmt.Fprintf(errOut, "unknown toolchain command %q", sub)
		if s := suggest(sub, toolchainSubcommands); s != "" {
			fmt.Fprintf(errOut, " (did you mean %q?)", s)
		}
		fmt.Fprintf(errOut, "\n\n%s", commandHelp["toolchain"])
		return 2
	}
	f, rc := parseTCFlags(sub, rest, errOut)
	if rc != 0 {
		return rc
	}
	switch sub {
	case "set-path":
		return tcSetPath(f, out, errOut, env, osName)
	case "resume":
		return tcResume(f, out, errOut, opts, env, osName)
	case "status":
		if f.tool == "" {
			return tcStatusAll(f, out, errOut, opts, env, osName)
		}
	default:
		if f.tool == "" {
			fmt.Fprintf(errOut, "toolchain %s needs a tool, e.g. zedrun-go toolchain %s gcc\n", sub, sub)
			return 2
		}
	}
	if _, ok := catalog.Tools[f.tool]; !ok {
		fmt.Fprintf(errOut, "unknown tool %q\n", f.tool)
		return 2
	}

	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	st, err := toolchain.Detect(context.Background(), cfg, cache, env, osName, f.tool, isRoot())
	_ = cache.Save()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	topts := toolchain.Options{Shell: f.shell, CacheDir: platform.CacheDir(env, osName)}
	d := toolchain.Diagnose(st, topts)

	switch sub {
	case "status":
		if f.json {
			return jsonOut(out, errOut, toolchain.Report{Status: st, Diagnosis: d})
		}
		toolchain.RenderStatus(out, st, d)
		return 0

	case "diagnose":
		p := toolchain.Repair(st, d, topts)
		tcRecord(f, opts, env, osName, st, d, p, errOut)
		if f.json {
			rc := jsonOut(out, errOut, map[string]any{"status": st, "diagnosis": d, "plan": p})
			if rc == 0 && d.Kind != toolchain.KindOK {
				return 1
			}
			return rc
		}
		toolchain.RenderStatus(out, st, d)
		if d.Kind != toolchain.KindOK {
			fmt.Fprintln(out)
			toolchain.RenderPlan(out, p)
			return 1
		}
		return 0

	case "how-to":
		p := toolchain.Repair(st, d, topts)
		if f.json {
			return jsonOut(out, errOut, p)
		}
		toolchain.RenderPlan(out, p)
		return 0

	case "verify":
		r := toolchain.Verify(st, d)
		p := toolchain.Repair(st, d, topts)
		tcRecord(f, opts, env, osName, st, d, p, errOut)
		if f.json {
			rc := jsonOut(out, errOut, r)
			if rc == 0 && !r.OK {
				return 1
			}
			return rc
		}
		if r.OK {
			fmt.Fprintf(out, "ok: %s %s at %s\n", r.Tool, r.Version, r.Path)
			return 0
		}
		fmt.Fprintf(out, "not verified: %s (%s)\n", r.Detail, r.Kind)
		return 1

	case "repair":
		return tcRepair(f, st, d, topts, out, errOut, opts, cfg, env, osName)
	}
	return 2
}

// tcRepair is the shape-(a) path: the tool does the fix when it honestly can,
// and otherwise hands back the exact instructions. It never prompts for a
// password and never elevates beyond what provision.Elevate allows.
func tcRepair(f tcFlags, st toolchain.Status, d toolchain.Diagnosis, topts toolchain.Options, out, errOut io.Writer, opts Options, cfg interface{}, env map[string]string, osName string) int {
	p := toolchain.Repair(st, d, topts)
	if d.Kind == toolchain.KindOK {
		if f.json {
			return jsonOut(out, errOut, map[string]any{"diagnosis": d, "plan": p, "ran": false})
		}
		fmt.Fprintf(out, "nothing to repair: %s\n", p.Summary)
		return 0
	}

	// Only a not-installed tool with a runnable route is something zedrun-go
	// may execute. Every other kind is a PATH or privilege problem whose fix
	// belongs to the user.
	if !p.CanRun || !f.run {
		tcRecord(f, opts, env, osName, st, d, p, errOut)
		if f.json {
			rc := jsonOut(out, errOut, map[string]any{"diagnosis": d, "plan": p, "ran": false})
			if rc == 0 {
				return 1
			}
			return rc
		}
		toolchain.RenderPlan(out, p)
		if p.CanRun && !f.run {
			fmt.Fprintln(out, "\nre-run with --run --yes to let zedrun-go do this")
			return 0
		}
		return 1
	}

	route := *p.Route
	if f.using != "" {
		m, ok := pickMethod(st.Routes, f.using)
		if !ok {
			for _, r := range st.Routes {
				if r.Downloadable() && r.Manager == f.using {
					m, ok = r, true
				}
			}
		}
		if !ok {
			fmt.Fprintf(errOut, "no usable %s route for %s on %s\n", f.using, st.Tool, osName)
			return 1
		}
		route = m
	}
	if !f.yes {
		fmt.Fprintf(out, "about to run:\n  %s\n\n%s\nre-run with --yes to confirm\n", route.Command(), p.Confirm)
		return 0
	}

	var rc int
	if route.Downloadable() {
		rc = execDownload(route, out, errOut, opts, env, osName, f.timeout)
	} else {
		rc = execMethod(route, "installing", out, errOut, opts, env, osName, f.timeout)
	}
	if rc != 0 {
		fmt.Fprintf(errOut, "the install exited %d; the toolchain was not changed\n", rc)
		return 1
	}

	// Never claim success from the install's exit code: probe again.
	cache := cacheFor(opts, env, osName)
	cache.ResetDirs() // the installer just changed the machine
	cf, _ := loadConfig(opts, env, osName)
	st2, err := toolchain.Detect(context.Background(), cf, cache, env, osName, st.Tool, isRoot())
	_ = cache.Save()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	d2 := toolchain.Diagnose(st2, topts)
	p2 := toolchain.Repair(st2, d2, topts)
	tcRecord(f, opts, env, osName, st2, d2, p2, errOut)
	r := toolchain.Verify(st2, d2)
	if r.OK {
		fmt.Fprintf(out, "\nverified: %s %s at %s\n", r.Tool, r.Version, r.Path)
		return 0
	}
	fmt.Fprintf(out, "\nthe install finished but %s is still not usable in this shell (%s)\n\n", st.Tool, d2.Kind)
	toolchain.RenderPlan(out, p2)
	return 1
}

func tcStatusAll(f tcFlags, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	reports := toolchain.Scan(context.Background(), cfg, cache, env, osName, isRoot(), nil)
	_ = cache.Save()
	bad := toolchain.Unhealthy(reports)
	if f.json {
		return jsonOut(out, errOut, map[string]any{"checked": len(reports), "unhealthy": bad})
	}
	if len(bad) == 0 {
		fmt.Fprintf(out, "%d installed toolchains checked, all resolve from PATH\n", len(reports))
		return 0
	}
	fmt.Fprintf(out, "%d of %d installed toolchains are unhealthy\n", len(bad), len(reports))
	for _, r := range bad {
		fmt.Fprintf(out, "  %-10s %-14s %s\n", r.Status.Tool, r.Diagnosis.Kind, r.Diagnosis.Cause)
	}
	fmt.Fprintln(out, "\nzedrun-go toolchain how-to TOOL   for the exact fix")
	return 0
}

// tcSetPath is shape (b): print the platform-correct, shell-correct line. The
// tool never writes it, because editing a user's profile unasked is how a
// setup tool becomes the next thing that needs fixing.
func tcSetPath(f tcFlags, out, errOut io.Writer, env map[string]string, osName string) int {
	if f.tool == "" {
		fmt.Fprintln(errOut, "toolchain set-path needs a directory, e.g. zedrun-go toolchain set-path /opt/llvm/bin")
		return 2
	}
	dir := f.tool
	if !isAbsFor(dir, osName) {
		fmt.Fprintf(errOut, "%q is not an absolute path; a relative PATH entry breaks as soon as you change directory\n", dir)
		return 2
	}
	if osName != "windows" {
		if _, err := os.Stat(dir); err != nil {
			fmt.Fprintf(errOut, "warning: %s does not exist on this machine\n", dir)
		}
	}
	shell := f.shell
	if shell == toolchain.ShellUnknown {
		shell = toolchain.DetectShell(env, osName)
	}
	shells := []toolchain.Shell{shell}
	if shell == toolchain.ShellUnknown {
		shells = toolchain.ShellsFor(osName)
	}
	if f.json {
		var lines []toolchain.PathLine
		for _, s := range shells {
			lines = append(lines, toolchain.PathAppend(dir, s, osName))
		}
		return jsonOut(out, errOut, lines)
	}
	for _, s := range shells {
		pl := toolchain.PathAppend(dir, s, osName)
		fmt.Fprintf(out, "%s\n", s)
		if pl.File != "" {
			fmt.Fprintf(out, "append to %s:\n", pl.File)
		} else {
			fmt.Fprintln(out, "run once:")
		}
		fmt.Fprintf(out, "```\n%s\n```\n", pl.Line)
		if pl.Session != "" && pl.Session != pl.Line {
			fmt.Fprintf(out, "this window only:\n```\n%s\n```\n", pl.Session)
		}
		if pl.Reload != "" {
			fmt.Fprintf(out, "then: %s\n", pl.Reload)
		}
		if pl.Note != "" {
			fmt.Fprintf(out, "note: %s\n", pl.Note)
		}
		fmt.Fprintln(out)
	}
	return 0
}

// tcResume prints the handoff record so a new session can continue from it.
func tcResume(f tcFlags, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	proj := tcProject(f, opts)
	path := toolchain.StatePath(env, osName, proj)
	r, ok, err := toolchain.Load(path)
	if err != nil {
		fmt.Fprintf(errOut, "ignoring %s: %v\nstart over with: zedrun-go toolchain diagnose TOOL\n", path, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(out, "no resume state for %q (looked for %s)\nstart with: zedrun-go toolchain diagnose TOOL\n", proj, path)
		return 1
	}
	if f.json {
		return jsonOut(out, errOut, map[string]any{"path": path, "state": r})
	}
	fmt.Fprintf(out, "project %s  state %s  (%s)\n", r.Project, r.State, path)
	if r.Machine != "" || r.Shell != "" {
		fmt.Fprintf(out, "machine %s  shell %s\n", r.Machine, r.Shell)
	}
	for _, s := range r.Subjects {
		fmt.Fprintf(out, "  #%d [%s] %s: %s\n", s.ID, s.Status, s.Tool, s.Symptom)
		if s.Fix != "" && s.Status != "done" {
			fmt.Fprintf(out, "      fix:    %s\n", s.Fix)
		}
		if s.LastResult != "" {
			fmt.Fprintf(out, "      result: %s\n", s.LastResult)
		}
	}
	if next, ok := r.NextPending(); ok {
		fmt.Fprintf(out, "\nnext: re-verify subject #%d (%s) with a fresh probe before changing anything\n", next.ID, next.Tool)
	} else {
		fmt.Fprintln(out, "\nnothing pending")
	}
	return 0
}

// tcProject names the resume file: --project, else the directory the user is
// working in.
func tcProject(f tcFlags, opts Options) string {
	if f.project != "" {
		return f.project
	}
	start := opts.ProjectStart
	if start == "" {
		start, _ = os.Getwd()
	}
	return filepath.Base(start)
}

// tcRecord folds a diagnosis into the project's resume file. It is best
// effort: a state file that cannot be written must never fail the repair, so
// problems are reported as warnings.
func tcRecord(f tcFlags, opts Options, env map[string]string, osName string, st toolchain.Status, d toolchain.Diagnosis, p toolchain.Plan, errOut io.Writer) {
	if f.noState {
		return
	}
	proj := tcProject(f, opts)
	path := toolchain.StatePath(env, osName, proj)
	if path == "" {
		return
	}
	r, err := toolchain.LoadOrNew(path, proj)
	if err != nil {
		fmt.Fprintf(errOut, "warning: ignoring unreadable resume state %s: %v\n", path, err)
	}
	r.Record(st, d, p, machineID(env, osName))
	if err := r.Save(path); err != nil {
		fmt.Fprintf(errOut, "warning: could not save resume state: %v\n", err)
	}
}

func machineID(env map[string]string, osName string) string {
	host, _ := os.Hostname()
	user := platform.Env(env, osName, "USER")
	if user == "" {
		user = platform.Env(env, osName, "USERNAME")
	}
	switch {
	case host != "" && user != "":
		return host + `\` + user
	case host != "":
		return host
	}
	return user
}

func jsonOut(out, errOut io.Writer, v any) int {
	if err := toolchain.JSON(out, v); err != nil {
		fmt.Fprintln(errOut, "json:", err)
		return 1
	}
	return 0
}

// isAbsFor reports whether p is absolute on the target OS, not the host's:
// `C:\tools` is absolute for a Windows answer even when asked on Linux.
func isAbsFor(p, osName string) bool {
	if osName == "windows" {
		return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

var _ = provision.Known // the toolchain commands share provision's route table
