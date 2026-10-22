// Package cli parses arguments and dispatches zedrun-go commands. Main never
// calls os.Exit, so tests drive it directly; the command path is shared with the
// TUI.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/doctor"
	"github.com/zedrun/zedrun-go/internal/executor"
	"github.com/zedrun/zedrun-go/internal/installer"
	"github.com/zedrun/zedrun-go/internal/planner"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/provision"
	"github.com/zedrun/zedrun-go/internal/sources"
	"github.com/zedrun/zedrun-go/internal/tui"
	"github.com/zedrun/zedrun-go/internal/version"
	"github.com/zedrun/zedrun-go/internal/versions"
)

// Options lets a caller inject the environment and network access.
type Options struct {
	Env          map[string]string
	OS           string
	Cache        *detect.ProbeCache
	Fetcher      *sources.Fetcher
	File         string
	ProjectStart string
}

var commandNames = []string{
	"run", "which", "doctor", "outdated", "use", "pick",
	"install", "uninstall", "languages", "clean", "get",
}

const usage = `zedrun-go — Code Runner for Zed

usage: zedrun-go <command> [options]
         zedrun-go [--file PATH]          open the dashboard

commands:
  run        run a file                     run FILE [args…] [--release] [--tool T] [--timeout S]
  which      show the toolchain a file would use
  doctor     diagnostics                    [--json] [--sources] [--all]
  outdated   compare installed vs latest    [--json] [--pre] [--fail-on-outdated]
  use        set a language preference      use LANGUAGE TOOL [--path P] [--config C]
  pick       choose a toolchain for a language
  install    install into Zed               [--dry-run] [--zed-dir D]
  uninstall  remove from Zed                [--purge]
  get        how to install a toolchain     get [TOOL] [--all] [--run] [--using M] [--yes]
  languages  list supported languages
  clean      clear the zedrun-go cache

Running with no command opens the TUI.
`

// Main runs zedrun-go with the given arguments and returns its exit code.
func Main(args []string, out, errOut io.Writer) int {
	return MainWith(args, out, errOut, Options{})
}

// MainWith is Main with injectable dependencies.
func MainWith(args []string, out, errOut io.Writer, opts Options) int {
	env := opts.Env
	if env == nil {
		env = environ()
	}
	osName := opts.OS
	if osName == "" {
		osName = platform.OSName("")
	}

	// `zedrun-go --file PATH` opens the dashboard already knowing what to run.
	// Options.File is the first source the dashboard's `r` key tries, and this
	// is the only thing that sets it, so without this that fallback is dead.
	if file, rest, ferr := parseFileFlag(args); ferr != nil {
		fmt.Fprintln(errOut, ferr)
		return 2
	} else if file != "" {
		opts.File = file
		args = rest
	}
	if len(args) == 0 {
		return cmdTUI(out, errOut, opts, env, osName)
	}
	// Answer `zedrun-go <cmd> --help` before the command's own flag parser can
	// reject it. Answering the most obvious question with "unknown flag" is a
	// dead end.
	if len(args) > 1 && wantsHelp(args[1:]) {
		if h, ok := commandHelp[args[0]]; ok {
			fmt.Fprint(out, h)
			return 0
		}
	}
	switch args[0] {
	case "--version":
		fmt.Fprintf(out, "zedrun-go %s\n", version.Version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return 0
	case "run":
		return cmdRun(args[1:], out, errOut, opts, env, osName)
	case "which":
		return cmdWhich(args[1:], out, errOut, opts, env, osName)
	case "doctor":
		return cmdDoctor(args[1:], out, errOut, opts, env, osName)
	case "outdated":
		return cmdOutdated(args[1:], out, errOut, opts, env, osName)
	case "use":
		return cmdUse(args[1:], out, errOut, opts, env, osName)
	case "pick":
		return cmdPick(args[1:], out, errOut, opts, env, osName)
	case "install":
		return cmdInstall(args[1:], out, errOut, opts, env, osName)
	case "uninstall":
		return cmdUninstall(args[1:], out, errOut, opts, env, osName)
	case "languages":
		return cmdLanguages(args[1:], out, errOut)
	case "clean":
		return cmdClean(args[1:], out, errOut, opts, env, osName)
	case "get":
		return cmdGet(args[1:], out, errOut, opts, env, osName)
	case "toolchain":
		return cmdToolchain(args[1:], out, errOut, opts, env, osName)
	}
	if looksLikePath(args[0]) {
		return cmdRun(args, out, errOut, opts, env, osName)
	}
	return unknownCommand(args[0], errOut)
}

// looksLikePath decides whether a bare first argument is a file to run rather
// than a mistyped command.
func looksLikePath(s string) bool {
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	if _, ok := catalog.LanguageForPath(s, nil); ok {
		return true
	}
	return false
}

func unknownCommand(cmd string, errOut io.Writer) int {
	fmt.Fprintf(errOut, "unknown command: %s\n", cmd)
	if s := suggest(cmd, commandNames); s != "" {
		fmt.Fprintf(errOut, "did you mean %q?\n", s)
	}
	fmt.Fprint(errOut, usage)
	return 2
}

// suggest returns the closest known command within a small edit distance.
func suggest(word string, candidates []string) string {
	best, bestD := "", 1<<30
	for _, c := range candidates {
		d := levenshtein(word, c)
		if d < bestD {
			bestD, best = d, c
		}
	}
	if bestD <= 3 {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func loadConfig(opts Options, env map[string]string, osName string) (config.Config, []string) {
	start := opts.ProjectStart
	if start == "" {
		start, _ = os.Getwd()
	}
	return config.Load(start, env, osName)
}

func cacheFor(opts Options, env map[string]string, osName string) *detect.ProbeCache {
	if opts.Cache != nil {
		return opts.Cache
	}
	return detect.NewProbeCache(filepath.Join(platform.CacheDir(env, osName), "probe.json"))
}

func buildDir(env map[string]string, osName string) string {
	return filepath.Join(platform.CacheDir(env, osName), "build")
}

// languagesMap layers the user's custom languages over the builtins.
func languagesMap(cfg config.Config) map[string]catalog.Language {
	if len(cfg.Languages) == 0 {
		return nil
	}
	out := make(map[string]catalog.Language, len(catalog.Languages)+len(cfg.Languages))
	for k, v := range catalog.Languages {
		out[k] = v
	}
	for k, spec := range cfg.Languages {
		if m, ok := spec.(map[string]any); ok {
			out[k] = catalog.BuildCustom(k, m)
		}
	}
	return out
}

type runFlags struct {
	release  bool
	tool     string
	timeout  time.Duration
	buildDir string
	dryRun   bool
	verbose  bool
}

func parseRunArgs(args []string) (file string, progArgs []string, f runFlags, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			progArgs = append(progArgs, args[i+1:]...)
			return file, progArgs, f, nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			if file == "" {
				file = a
			} else {
				progArgs = append(progArgs, a)
			}
			continue
		}
		need := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "--release":
			f.release = true
		case "--dry-run":
			f.dryRun = true
		case "-v", "--verbose":
			f.verbose = true
		case "--tool":
			v, e := need()
			if e != nil {
				return "", nil, f, e
			}
			f.tool = v
		case "--build-dir":
			v, e := need()
			if e != nil {
				return "", nil, f, e
			}
			f.buildDir = v
		case "--timeout":
			v, e := need()
			if e != nil {
				return "", nil, f, e
			}
			n, convErr := time.ParseDuration(v)
			if convErr != nil {
				secs, secErr := parseSeconds(v)
				if secErr != nil {
					return "", nil, f, fmt.Errorf("bad --timeout %q", v)
				}
				n = time.Duration(secs) * time.Second
			}
			f.timeout = n
		default:
			return "", nil, f, fmt.Errorf("unknown flag %s", a)
		}
	}
	return file, progArgs, f, nil
}

func parseSeconds(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func cmdRun(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	file, progArgs, flags, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if file == "" {
		fmt.Fprintln(errOut, "zedrun-go run: a file is required")
		return 2
	}
	abs, aerr := filepath.Abs(file)
	if aerr != nil {
		abs = file
	}
	if fi, serr := os.Stat(abs); serr != nil || fi.IsDir() {
		fmt.Fprintf(errOut, "file not found: %s\n", file)
		return 1
	}

	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	langs := languagesMap(cfg)
	lang, ok := catalog.LanguageForPath(abs, langs)
	if !ok {
		fmt.Fprintf(errOut, "no language found for %s\n", file)
		return 1
	}

	cache := cacheFor(opts, env, osName)
	res, rerr := planner.Resolve(lang, abs, cfg, cache, flags.tool, env, osName, langs)
	if rerr != nil {
		if errors.Is(rerr, planner.ErrNoToolchain) {
			fmt.Fprintf(errOut, "%v\n", rerr)
			fmt.Fprintf(errOut, "hint: %s\n", installHintFor(lang, osName, langs))
			return 127
		}
		fmt.Fprintln(errOut, rerr)
		return 2
	}

	fmt.Fprintf(out, "%s %s (%s) %s\n", res.Install.Tool, res.Install.Version, res.Install.Origin, res.Install.Path)
	for _, alt := range res.Alternatives {
		if alt.OK && alt.Path != res.Install.Path {
			fmt.Fprintf(out, "  also: %s %s (%s) %s\n", alt.Tool, alt.Version, alt.Origin, alt.Path)
		}
	}

	dir := flags.buildDir
	if dir == "" {
		dir = buildDir(env, osName)
	}
	plan := planner.BuildPlan(abs, res, cfg, flags.release, progArgs, dir)
	if flags.dryRun {
		for _, st := range plan.Steps {
			fmt.Fprintf(out, "  (in %s) %s\n", st.Cwd, strings.Join(st.Argv, " "))
		}
		return 0
	}
	code := executor.Execute(plan, cfg, out, flags.timeout, cfg.EchoCommands, flags.verbose)
	_ = cache.Save()
	return code
}

func installHintFor(lang, osName string, langs map[string]catalog.Language) string {
	m := langs
	if m == nil {
		m = catalog.Languages
	}
	if l, ok := m[lang]; ok && len(l.Runners) > 0 {
		return catalog.InstallHint(l.Runners[0].Tool[0], osName)
	}
	return "install a toolchain for " + lang
}

func cmdWhich(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	file, _, flags, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if file == "" {
		fmt.Fprintln(errOut, "zedrun-go which: a file is required")
		return 2
	}
	abs, aerr := filepath.Abs(file)
	if aerr != nil {
		abs = file
	}
	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	langs := languagesMap(cfg)
	lang, ok := catalog.LanguageForPath(abs, langs)
	if !ok {
		fmt.Fprintf(errOut, "no language found for %s\n", file)
		return 1
	}
	res, rerr := planner.Resolve(lang, abs, cfg, cacheFor(opts, env, osName), flags.tool, env, osName, langs)
	if rerr != nil {
		fmt.Fprintln(errOut, rerr)
		return 127
	}
	fmt.Fprintf(out, "%s: %s\n", file, lang)
	fmt.Fprintf(out, "  tool     %s\n", res.Install.Tool)
	fmt.Fprintf(out, "  version  %s\n", res.Install.Version)
	fmt.Fprintf(out, "  origin   %s\n", res.Install.Origin)
	fmt.Fprintf(out, "  path     %s\n", res.Install.Path)
	for _, alt := range res.Alternatives {
		if alt.OK && alt.Path != res.Install.Path {
			fmt.Fprintf(out, "  also     %s %s (%s) %s\n", alt.Tool, alt.Version, alt.Origin, alt.Path)
		}
	}
	return 0
}

func cmdDoctor(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var jsonOut, checkSources, showAll bool
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--sources":
			checkSources = true
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(errOut, "unknown flag %s\n", a)
			return 2
		}
	}
	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	report := doctor.Collect(cfg, cache, env, osName, nil)
	_ = cache.Save()

	if checkSources {
		f := opts.Fetcher
		if f == nil {
			f = sources.NewFetcher(filepath.Join(platform.CacheDir(env, osName), "sources.json"), 6*time.Hour, nil)
		}
		var results []map[string]any
		for _, r := range sources.CheckEndpoints(f, sources.Products) {
			results = append(results, map[string]any{"product": r.Product, "url": r.URL, "ok": r.OK, "note": r.Note})
		}
		report["sources"] = results
	}

	if jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		fmt.Fprintln(out, string(data))
		return 0
	}
	doctor.Render(report, out, showAll)
	if checkSources {
		fmt.Fprintln(out, "\nSources")
		for _, r := range report["sources"].([]map[string]any) {
			status := "ok"
			if r["ok"] == false {
				status = "FAILED"
			}
			fmt.Fprintf(out, "  %-8s %-6s %s\n", r["product"], status, r["note"])
		}
	}
	return 0
}

func cmdOutdated(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var jsonOut, pre, failOn bool
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--pre":
			pre = true
		case "--fail-on-outdated":
			failOn = true
		default:
			fmt.Fprintf(errOut, "unknown flag %s\n", a)
			return 2
		}
	}
	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	f := opts.Fetcher
	if f == nil {
		f = sources.NewFetcher(filepath.Join(platform.CacheDir(env, osName), "sources.json"), 6*time.Hour, nil)
	}
	channel := "stable"
	if pre {
		channel = "pre"
	}

	ids := make([]string, 0, len(sources.Products))
	for id := range sources.Products {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	type row = outdatedRow
	var rows []row
	anyOutdated := false
	detected := provision.Detected(cfg, cache, env, osName)
	// One `winget list` call serves the whole report: the index is memoised, so a
	// report touching ten winget packages still asks winget once.
	wingetIndex := provision.WingetIndexFunc(env, osName)
	for _, id := range ids {
		p := sources.Products[id]
		inst, ok := firstInstall(p, cfg, cache, env, osName)
		if !ok {
			continue
		}
		state, target, bump := versions.Judge(inst.Version, sources.FetchLatest(p, f), channel)
		if state == "outdated" {
			anyOutdated = true
		}
		// "outdated" on its own is a complaint. Say what would fix it, and say
		// it through the same engine the get --update command uses, so the two
		// cannot disagree about how a tool is updated.
		update, reason := "", ""
		if state == "outdated" {
			origin := ""
			for _, in := range detect.FindInstalls(catalog.GetTool(inst.Tool), cfg, cache, false, env, osName) {
				if in.OK {
					origin = in.Origin
					break
				}
			}
			plan := provision.ResolveUpdate(inst.Tool, osName, inst.Version, target, origin, detected, env, wingetIndex())
			update, reason = updHint(plan, inst.Version, target)
		}
		rows = append(rows, row{tool: inst.Tool, installed: inst.Version, latest: target, status: state, bump: bump, update: update, reason: reason})
	}
	_ = cache.Save()

	if jsonOut {
		list := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			list = append(list, map[string]string{
				"tool": r.tool, "installed": r.installed, "latest": r.latest,
				"status": r.status, "bump": r.bump, "update": r.update,
				"reason": r.reason,
			})
		}
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		fmt.Fprintln(out, string(data))
	} else {
		fmt.Fprintf(out, "%-8s %-12s %-12s %-11s %s\n", "Tool", "Installed", "Latest", "Status", "Bump")
		fmt.Fprintln(out, strings.Repeat("-", 52))
		for _, r := range rows {
			fmt.Fprintf(out, "%-8s %-12s %-12s %-11s %s\n",
				r.tool, orDash(r.installed), orDash(r.latest), r.status, r.bump)
			if r.update != "" {
				fmt.Fprintf(out, "%-8s %s\n", "", "update: "+r.update)
			} else if r.reason != "" {
				fmt.Fprintf(out, "%-8s %s\n", "", r.reason)
			}
		}
	}
	if failOn && anyOutdated {
		return 1
	}
	return 0
}

// outdatedRow is one line of the `outdated` report. update is the command that
// would fix an outdated tool, empty when nothing on this machine can do it; reason
// says why when it is empty.
type outdatedRow struct{ tool, installed, latest, status, bump, update, reason string }

// updHint renders what an update would do: the command, plus the version it will
// actually install when the manager has said so.
//
// The achievable version is the point. winget's own answer can be older than
// upstream's — its Go manifest sits at 1.27.0 while go.dev has 1.27.3 — and
// promising upstream's number when the command lands the older one is the bug
// this exists to prevent. NextVersion also refuses a version that belongs to a
// different product, because Git's 2.55.0.5 is Git's version and not bash's
// next one.
func updHint(plan provision.UpdateRoute, installed, upstream string) (update, reason string) {
	if plan.Method.Runnable() {
		update = plan.Method.Command()
		if next := plan.NextVersion(installed); next != "" {
			update += " → " + next
			// "lags" is a claim about versions, not strings: winget writes
			// 7.6.7.0 where upstream writes 7.6.7, and those are the same
			// release.
			if !versions.SameVersion(next, upstream) {
				update += " (" + plan.Method.Manager + " lags upstream " + upstream + ")"
			}
		}
		return update, ""
	}
	return "", plan.Reason
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func firstInstall(p sources.Product, cfg config.Config, cache *detect.ProbeCache, env map[string]string, osName string) (detect.Install, bool) {
	for _, id := range p.Tools {
		for _, in := range detect.FindInstalls(catalog.GetTool(id), cfg, cache, false, env, osName) {
			if in.OK {
				return in, true
			}
		}
	}
	return detect.Install{}, false
}

func cmdUse(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var cfgPath, toolPath string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--config needs a value")
				return 2
			}
			i++
			cfgPath = args[i]
		case "--path":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--path needs a value")
				return 2
			}
			i++
			toolPath = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(errOut, "unknown flag %s\n", args[i])
				return 2
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 2 {
		fmt.Fprintln(errOut, "zedrun-go use: LANGUAGE TOOL are required")
		return 2
	}
	language, tool := positional[0], positional[1]
	if cfgPath == "" {
		cfgPath = config.UserConfigPath(env, osName)
	}
	msg, err := config.SetPreference(cfgPath, language, tool, toolPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, msg)
	return 0
}

func cmdPick(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(errOut, "unknown flag %s\n", a)
			return 2
		}
		positional = append(positional, a)
	}
	if len(positional) < 1 {
		fmt.Fprintln(errOut, "zedrun-go pick: a language is required")
		return 2
	}
	language := positional[0]
	lang, ok := catalog.Languages[language]
	if !ok {
		fmt.Fprintf(errOut, "unknown language %q\n", language)
		return 2
	}
	cfg, _ := loadConfig(opts, env, osName)
	cache := cacheFor(opts, env, osName)
	fmt.Fprintf(out, "%s — installed toolchains:\n", language)
	seen := map[string]bool{}
	ambiguous := false
	for _, r := range lang.Runners {
		for _, id := range r.Tool {
			if seen[id] {
				continue
			}
			seen[id] = true
			installs := detect.FindInstalls(catalog.GetTool(id), cfg, cache, true, env, osName)
			if len(installs) == 0 {
				fmt.Fprintf(out, "    %-10s not installed — %s\n", id, catalog.InstallHint(id, osName))
				continue
			}
			if len(installs) > 1 {
				ambiguous = true
			}
			for _, in := range installs {
				state := "ok"
				if !in.OK {
					state = orDash(in.Note)
				}
				fmt.Fprintf(out, "  * %-10s %-10s %-12s %s\n", in.Tool, orDash(in.Version), state, in.Path)
			}
		}
	}
	// When two installs share a tool id, `use <lang> <tool>` cannot separate
	// them: the preference is keyed by tool id, which is exactly what collides.
	// Only the path distinguishes them, so the hint has to say so.
	if ambiguous {
		fmt.Fprintf(out, "\npin one with: zedrun-go use %s <tool> --path <path>\n", language)
	} else {
		fmt.Fprintf(out, "\nchoose one with: zedrun-go use %s <tool>\n", language)
	}
	_ = cache.Save()
	return 0
}

func cmdInstall(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var dryRun bool
	zedDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--zed-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--zed-dir needs a value")
				return 2
			}
			i++
			zedDir = args[i]
		default:
			fmt.Fprintf(errOut, "unknown flag %s\n", args[i])
			return 2
		}
	}
	if zedDir == "" {
		zedDir = platform.ZedConfigDir(env, osName)
	}
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "zedrun-go"
	}
	if _, err := installer.Install(zedDir, bin, dryRun, out); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if dryRun {
		fmt.Fprintln(out, "(dry run — nothing was written)")
		return 0
	}
	fmt.Fprintln(out, "installed. Restart Zed if it was running.")
	return 0
}

func cmdUninstall(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var purge bool
	zedDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--purge":
			purge = true
		case "--zed-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--zed-dir needs a value")
				return 2
			}
			i++
			zedDir = args[i]
		default:
			fmt.Fprintf(errOut, "unknown flag %s\n", args[i])
			return 2
		}
	}
	if zedDir == "" {
		zedDir = platform.ZedConfigDir(env, osName)
	}
	msgs, err := installer.Uninstall(zedDir, purge)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if len(msgs) == 0 {
		fmt.Fprintln(out, "nothing to remove")
		return 0
	}
	for _, m := range msgs {
		fmt.Fprintln(out, m)
	}
	return 0
}

func cmdLanguages(args []string, out, errOut io.Writer) int {
	keys := make([]string, 0, len(catalog.Languages))
	for k := range catalog.Languages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(out, "%-12s %s\n", "Language", "Extensions")
	fmt.Fprintln(out, strings.Repeat("-", 44))
	for _, k := range keys {
		exts := strings.Join(catalog.Languages[k].Exts, ", ")
		if exts == "" {
			exts = "(by shebang)"
		}
		fmt.Fprintf(out, "%-12s %s\n", k, exts)
	}
	return 0
}

func cmdClean(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	dir := platform.CacheDir(env, osName)
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "cleared %s\n", dir)
	return 0
}

func cmdTUI(out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	if !isTTY() {
		report := doctor.Collect(cfg, cache, env, osName, nil)
		doctor.Render(report, out, false)
		return 0
	}
	code, err := tui.Run(cfg, tui.Options{
		Env: env, OS: osName, Cache: cache, Fetcher: tuiFetcher(opts, env, osName), File: opts.File,
	})
	_ = cache.Save()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return code
}

// tuiFetcher returns the version-source fetcher the dashboard should use. The
// TUI is normally launched with none, and without one every update check
// reports "no version source configured" — so build the same one `outdated`
// does rather than leaving the key dead.
func tuiFetcher(opts Options, env map[string]string, osName string) *sources.Fetcher {
	if opts.Fetcher != nil {
		return opts.Fetcher
	}
	return sources.NewFetcher(filepath.Join(platform.CacheDir(env, osName), "sources.json"), 6*time.Hour, nil)
}

// parseFileFlag pulls `--file PATH` or `--file=PATH` off the front of args and
// returns what is left. It exists so the dashboard's `r` key has a file to run.
func parseFileFlag(args []string) (file string, rest []string, err error) {
	if len(args) == 0 {
		return "", args, nil
	}
	switch {
	case args[0] == "--file":
		if len(args) < 2 {
			return "", nil, errors.New("--file needs a path")
		}
		return args[1], args[2:], nil
	case strings.HasPrefix(args[0], "--file="):
		return strings.TrimPrefix(args[0], "--file="), args[1:], nil
	}
	return "", args, nil
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func environ() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// cmdGet lists every way to obtain a toolchain on this machine and, with
// --run and --yes, executes one. Listing is the default and running needs
// --yes: starting a third-party installer without being asked is not something
// this tool does. --update does the same for a tool that is already installed.
func cmdGet(args []string, out, errOut io.Writer, opts Options, env map[string]string, osName string) int {
	var (
		tool    string
		all     bool
		doRun   bool
		update  bool
		yes     bool
		using   string
		timeout time.Duration
	)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--all":
			all = true
		case "--run":
			doRun = true
		case "--update":
			update = true
		case "--yes", "-y":
			yes = true
		case "--using":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--using needs a manager name")
				return 2
			}
			i++
			using = args[i]
		case "--timeout":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--timeout needs a value, like --timeout 2m")
				return 2
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil || d <= 0 {
				fmt.Fprintf(errOut, "bad --timeout %q\n", args[i])
				return 2
			}
			timeout = d
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(errOut, "unknown flag %s\n", a)
				return 2
			}
			if tool != "" {
				fmt.Fprintf(errOut, "get takes one tool, got %q and %q\n", tool, a)
				return 2
			}
			tool = a
		}
	}

	cfg, warns := loadConfig(opts, env, osName)
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	cache := cacheFor(opts, env, osName)
	detected := provision.Detected(cfg, cache, env, osName)
	_ = cache.Save()

	if tool == "" {
		return getMissing(out, errOut, cfg, cache, detected, env, osName, all)
	}
	if _, ok := catalog.Tools[tool]; !ok {
		fmt.Fprintf(errOut, "unknown tool %q\n", tool)
		return 2
	}

	ms := provision.Available(tool, osName, detected, all)
	ms = provision.Order(ms, provision.OwnerFor(installOrigin(tool, cfg, cache, env, osName)))

	if update {
		return getUpdate(tool, ms, using, out, errOut, opts, env, osName, timeout)
	}

	if !doRun {
		provision.Render(out, tool, ms)
		return 0
	}

	pick, ok := pickMethod(ms, using)
	if !ok {
		// A URL-only (direct) method is not runnable, but it is downloadable: zedrun
		// fetches the URL itself and shows a progress bar while it does. --yes
		// confirms the fetch. The selected --using manager is honored: the direct
		// route is used only when nothing else was chosen or it is the one chosen.
		for _, d := range ms {
			if d.Downloadable() && (using == "" || d.Manager == using) {
				if !yes {
					fmt.Fprintf(out, "about to download %s → %s\n\nre-run with --yes to confirm\n", d.Tool, d.URL)
					return 0
				}
				return execDownload(d, out, errOut, opts, env, osName, timeout)
			}
		}
		provision.Render(out, tool, ms)
		if using != "" {
			fmt.Fprintf(errOut, "\nno usable %s route for %s on %s\n", using, tool, osName)
		} else {
			fmt.Fprintf(errOut, "\nnothing runnable — pass --using <manager> to choose one\n")
		}
		return 1
	}
	if !yes {
		fmt.Fprintf(out, "about to run:\n  %s\n\nre-run with --yes to confirm\n", pick.Command())
		return 0
	}

	return execMethod(pick, "installing", out, errOut, opts, env, osName, timeout)
}

// execMethod runs a method, printing the exact command first and reporting the
// outcome in the tool's own exit code. Elevation is decided by elevateFor, so
// a method that cannot be elevated is explained rather than attempted.
// execDownload fetches a URL-only (direct) method into the cache with a progress
// bar and tells the user where it landed. It is the direct-download counterpart
// of execMethod: there is no manager argv to elevate, so the file is simply
// deposited and the post-install command is printed for the user.
func execDownload(m provision.Method, out, errOut io.Writer, opts Options, env map[string]string, osName string, timeout time.Duration) int {
	cacheDir := platform.CacheDir(env, osName)
	dest := filepath.Join(cacheDir, "downloads", m.Tool)
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_, err := provision.Download(ctx, nil, m.URL, dest, out)
	if err != nil {
		fmt.Fprintln(errOut, "download failed:", err)
		return 1
	}
	fmt.Fprintf(out, "downloaded %s to %s\nrun it with: %s\n", m.Tool, dest, m.Note)
	return 0
}

func execMethod(m provision.Method, verb string, out, errOut io.Writer, opts Options, env map[string]string, osName string, timeout time.Duration) int {
	root := isRoot()
	argv, ok, note := elevateFor(m, osName, root, env)
	if !ok {
		fmt.Fprintln(errOut, note)
		return 1
	}
	elevated := argv[0] == "sudo"
	fmt.Fprintf(out, "%s %s with %s\n", verb, m.Manager, m.Tool)
	if elevated {
		fmt.Fprintf(out, "elevated with sudo (passwordless)\n")
	}
	code := provision.Run(m, provision.RunOptions{
		Elevate: elevated,
		Env:     env,
		Stdout:  out,
		Stderr:  errOut,
		IsRoot:  root,
		OS:      osName,
		Timeout: timeout,
	})
	if code == 0 {
		fmt.Fprintf(out, "\n%s done. `zedrun-go which %s` will confirm it.\n", m.Tool, m.Tool)
	}
	return code
}

// getUpdate updates a tool that is already installed, through the manager that
// owns it. A tool with no manager is reported rather than silently re-installed.
func getUpdate(tool string, ms []provision.Method, using string, out, errOut io.Writer, opts Options, env map[string]string, osName string, timeout time.Duration) int {
	var candidates []provision.Method
	for _, m := range ms {
		if using != "" && m.Manager != using {
			continue
		}
		if argv, ok := m.Update(); ok {
			candidates = append(candidates, provision.Method{
				Tool: tool, Manager: m.Manager, Package: m.Package,
				Argv: argv, Elevated: m.Elevated, Present: m.Present,
			})
		}
	}
	if len(candidates) == 0 {
		if using != "" {
			fmt.Fprintf(errOut, "%s has no %s update route on %s\n", tool, using, osName)
		} else {
			fmt.Fprintf(errOut, "%s has no update route on %s — it is not managed by a known package manager\n", tool, osName)
		}
		return 1
	}
	pick := candidates[0]
	for _, m := range candidates {
		if m.Manager == using {
			pick = m
			break
		}
	}
	fmt.Fprintf(out, "%s: %s\n", pick.Manager, pick.Command())
	code := execMethod(pick, "updating", out, errOut, opts, env, osName, timeout)
	if code == 0 {
		fmt.Fprintf(out, "\n%s is up to date with its manager.\n", tool)
	}
	return code
}

// elevateFor is the CLI's elevation decision, delegated to provision so the
// TUI and the command line cannot drift apart on when sudo is used.
func elevateFor(m provision.Method, osName string, isRoot bool, env map[string]string) ([]string, bool, string) {
	return provision.Elevate(m, osName, isRoot, env)
}

// pickMethod chooses what --run would execute: the named manager if one was
// given, otherwise the first method that can actually be run.
func pickMethod(ms []provision.Method, using string) (provision.Method, bool) {
	for _, m := range ms {
		if !m.Runnable() {
			continue
		}
		if using == "" || m.Manager == using {
			return m, true
		}
	}
	return provision.Method{}, false
}

// installOrigin reports where the first usable install of a tool came from, so
// the manager that already owns it can be preferred.
func installOrigin(tool string, cfg config.Config, cache *detect.ProbeCache, env map[string]string, osName string) string {
	for _, in := range detect.FindInstalls(catalog.GetTool(tool), cfg, cache, false, env, osName) {
		if in.OK {
			return in.Origin
		}
	}
	return ""
}

// getMissing is the new-device flow: for every language with no usable
// toolchain, list the ways to get one.
func getMissing(out, errOut io.Writer, cfg config.Config, cache *detect.ProbeCache, detected map[string]string, env map[string]string, osName string, all bool) int {
	keys := make([]string, 0, len(catalog.Languages))
	for k := range catalog.Languages {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	missing := 0
	for _, lang := range keys {
		if _, err := planner.Resolve(lang, "", cfg, cache, "", env, osName, nil); err == nil {
			continue
		}
		missing++
		fmt.Fprintf(out, "\n%s\n", lang)
		for _, r := range catalog.Languages[lang].Runners {
			for _, id := range r.Tool {
				provision.Render(out, id, provision.Available(id, osName, detected, all))
				break
			}
			break
		}
	}
	if missing == 0 {
		fmt.Fprintln(out, "every language has a usable toolchain")
		return 0
	}
	fmt.Fprintf(out, "\n%d language(s) have nothing usable. `zedrun-go get <tool> --run --yes` installs one.\n", missing)
	return 0
}

// isRoot reports whether this process already has root, in which case an
// elevated method needs no sudo. On Windows Geteuid returns -1, so an elevated
// method there is explained rather than attempted.
var isRoot = func() bool { return os.Geteuid() == 0 }
