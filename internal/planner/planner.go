// Package planner turns a language + file into a concrete plan: which toolchain
// to use, which command steps to run, where the artifacts go, and which
// directories must be on PATH. It never runs anything.
package planner

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/versions"
)

// ErrNoToolchain is returned by Resolve when no runner has a usable toolchain.
var ErrNoToolchain = errors.New("no usable toolchain")

// Step is one command in a plan.
type Step struct {
	Argv  []string
	Cwd   string
	Label string
}

// Plan is everything the executor needs. Scripts are files the executor must
// write before the first step (used for the MSVC vcvars wrapper).
type Plan struct {
	Language   string
	Install    detect.Install
	Steps      []Step
	Env        map[string]string
	PathPrefix []string
	Writes     []string
	Scripts    map[string]string
	Release    bool
}

// Resolution is the chosen runner plus what was seen along the way.
type Resolution struct {
	Language     string
	Runner       catalog.Runner
	Install      detect.Install
	Alternatives []detect.Install
	Root         string
	ToolPaths    map[string]string // tool id -> path, for {tool:ID}
}

var spliceNames = map[string]bool{"flags": true, "profile": true, "std": true, "libs": true, "args": true}

// FindUp walks up from start looking for a file matching pattern, returning the
// directory that contains it.
func FindUp(start, pattern string) (string, bool) {
	dir := start
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if m, _ := filepath.Glob(filepath.Join(dir, pattern)); len(m) > 0 {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// CheckWhen evaluates one runner condition against the resolution context.
func CheckWhen(cond string, ctx map[string]any) bool {
	switch {
	case strings.HasPrefix(cond, "project:"):
		root, _ := ctx["root"].(string)
		if root == "" {
			return false
		}
		m, _ := filepath.Glob(filepath.Join(root, cond[len("project:"):]))
		return len(m) > 0
	case strings.HasPrefix(cond, "has:"):
		tools, _ := ctx["tools"].(map[string]bool)
		return tools[cond[len("has:"):]]
	case strings.HasPrefix(cond, "version>="):
		got, _ := ctx["version"].(string)
		gv, ok1 := versions.ParseVersion(got)
		wv, ok2 := versions.ParseVersion(cond[len("version>="):])
		if !ok1 || !ok2 {
			return false
		}
		return versions.Compare(gv, wv) >= 0
	case strings.HasPrefix(cond, "os:"):
		osName, _ := ctx["os"].(string)
		switch cond[len("os:"):] {
		case "posix":
			return osName == "linux" || osName == "macos"
		default:
			return osName == cond[len("os:"):]
		}
	}
	return true
}

// Expand turns one command template into argv. A whole element that names a
// splice is replaced by that splice's elements (so an empty splice disappears);
// every other element has its placeholders substituted.
func Expand(template []string, values map[string]string, splices map[string][]string, lookup func(string) string) []string {
	var out []string
	for _, el := range template {
		if name, ok := wholePlaceholder(el); ok && spliceNames[name] {
			out = append(out, splices[name]...)
			continue
		}
		out = append(out, substitute(el, values, lookup))
	}
	return out
}

func wholePlaceholder(el string) (string, bool) {
	if len(el) < 3 || el[0] != '{' || el[len(el)-1] != '}' {
		return "", false
	}
	name := el[1 : len(el)-1]
	if strings.ContainsAny(name, "{}") || strings.HasPrefix(name, "tool:") {
		return "", false
	}
	return name, true
}

func substitute(s string, values map[string]string, lookup func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		name := s[i+1 : i+j]
		switch {
		case strings.HasPrefix(name, "tool:"):
			if lookup != nil {
				if v := lookup(name[len("tool:"):]); v != "" {
					b.WriteString(v)
					break
				}
			}
			b.WriteString(s[i : i+j+1])
		default:
			if v, ok := values[name]; ok {
				b.WriteString(v)
			} else {
				b.WriteString(s[i : i+j+1])
			}
		}
		s = s[i+j+1:]
	}
}

// Resolve picks the runner to use: the first whose tools include a usable
// install and whose conditions hold. prefer moves a tool to the front, force
// restricts the choice to one tool.
func Resolve(language, file string, cfg config.Config, cache *detect.ProbeCache, forceTool string, env map[string]string, osOverride string, langs map[string]catalog.Language) (Resolution, error) {
	if langs == nil {
		langs = catalog.Languages
	}
	lang, ok := langs[language]
	if !ok {
		return Resolution{}, fmt.Errorf("unknown language %q", language)
	}
	fileDir := filepath.Dir(file)
	toolPaths := map[string]string{}
	var alternatives []detect.Install
	var tried []string

	for _, r := range orderRunners(lang.Runners, cfg.Prefer[language], forceTool) {
		var chosen detect.Install
		found := false
		toolOK := map[string]bool{}
		for _, id := range r.Tool {
			insts := detect.FindInstalls(catalog.GetTool(id), cfg, cache, false, env, osOverride)
			alternatives = append(alternatives, insts...)
			var usable *detect.Install
			for i := range insts {
				if insts[i].OK {
					usable = &insts[i]
					break
				}
			}
			if usable == nil {
				tried = append(tried, id)
				continue
			}
			toolOK[id] = true
			if _, seen := toolPaths[id]; !seen {
				toolPaths[id] = usable.Path
			}
			if !found {
				chosen = *usable
				found = true
			}
		}
		if !found {
			continue
		}

		ctx := map[string]any{
			"os":      platform.OSName(osOverride),
			"tools":   toolOK,
			"version": chosen.Version,
			"root":    "",
		}
		root := ""
		ok := true
		for _, cond := range r.When {
			if strings.HasPrefix(cond, "project:") {
				if rr, found := FindUp(fileDir, cond[len("project:"):]); found {
					root = rr
					ctx["root"] = rr
					continue
				}
				ok = false
				break
			}
			if !CheckWhen(cond, ctx) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		return Resolution{
			Language:     language,
			Runner:       r,
			Install:      chosen,
			Alternatives: alternatives,
			Root:         root,
			ToolPaths:    toolPaths,
		}, nil
	}
	return Resolution{}, fmt.Errorf("%w for %s (tried: %s)", ErrNoToolchain, language, strings.Join(dedupe(tried), ", "))
}

func orderRunners(runners []catalog.Runner, prefer, force string) []catalog.Runner {
	out := append([]catalog.Runner{}, runners...)
	if force != "" {
		var filtered []catalog.Runner
		for _, r := range out {
			if contains(r.Tool, force) {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) > 0 {
			return filtered
		}
		return out
	}
	if prefer != "" {
		var first, rest []catalog.Runner
		for _, r := range out {
			if contains(r.Tool, prefer) {
				first = append(first, r)
			} else {
				rest = append(rest, r)
			}
		}
		return append(first, rest...)
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// BuildPlan expands the chosen runner into concrete steps, values and artifacts.
func BuildPlan(file string, res Resolution, cfg config.Config, release bool, args []string, buildDir string) Plan {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	dir := filepath.Dir(abs)
	stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	exe, jar, obj := ArtifactPaths(abs, buildDir)
	root := res.Root
	if root == "" {
		root = dir
	}

	flags := append([]string{}, cfg.Flags[res.Language]...)
	if release {
		flags = append(flags, res.Runner.Release...)
	}
	std := res.Runner.Std
	if v := cfg.Std[res.Language]; v != "" {
		std = v
	}

	values := map[string]string{
		"file":       abs,
		"dir":        dir,
		"stem":       stem,
		"name":       filepath.Base(abs),
		"exe":        exe,
		"jar":        jar,
		"obj":        obj,
		"root":       root,
		"posix_file": platform.ToWSLPath(abs),
		"tool":       res.Install.Path,
	}
	splices := map[string][]string{
		"flags": flags,
		"std":   one(std),
		"libs":  one(res.Runner.Libs),
		"args":  args,
	}
	lookup := func(id string) string { return res.ToolPaths[id] }

	cwd := dir
	if res.Runner.Cwd == "{root}" {
		cwd = root
	}
	var steps []Step
	for _, tmpl := range res.Runner.Steps {
		argv := Expand(tmpl, values, splices, lookup)
		if len(argv) == 0 {
			continue
		}
		steps = append(steps, Step{Argv: argv, Cwd: cwd, Label: argv[0]})
	}

	scripts := map[string]string{}
	writes := artifactsIn(steps, exe, jar, obj)
	if res.Install.Tool == "msvc" {
		bat := filepath.Join(buildDir, "msvc-"+stem+".bat")
		scripts[bat] = msvcScript(res.Install.Path, steps)
		writes = append([]string{bat}, writes...)
		steps = []Step{{Argv: []string{"cmd", "/c", bat}, Cwd: cwd, Label: "msvc"}}
	}

	return Plan{
		Language:   res.Language,
		Install:    res.Install,
		Steps:      steps,
		Env:        cfg.Env,
		PathPrefix: []string{filepath.Dir(res.Install.Path)},
		Writes:     writes,
		Scripts:    scripts,
		Release:    release,
	}
}

func one(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func artifactsIn(steps []Step, exe, jar, obj string) []string {
	seen := map[string]bool{}
	var out []string
	for _, st := range steps {
		for _, a := range st.Argv {
			for _, cand := range []string{exe, jar, obj} {
				if a == cand && !seen[cand] {
					seen[cand] = true
					out = append(out, cand)
				}
			}
		}
	}
	return out
}

func msvcScript(clPath string, steps []Step) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	if vc := vcvarsPath(clPath); vc != "" {
		b.WriteString(`call "` + vc + `"` + "\r\n")
	}
	for _, st := range steps {
		b.WriteString(strings.Join(st.Argv, " "))
		b.WriteString("\r\n")
	}
	return b.String()
}

// vcvarsPath finds the VC root in a cl.exe path so the wrapper can call
// vcvars64.bat before compiling.
func vcvarsPath(clPath string) string {
	slashed := strings.ReplaceAll(clPath, `\`, "/")
	if i := strings.Index(strings.ToLower(slashed), "/vc/"); i >= 0 {
		return slashed[:i] + "/VC/Auxiliary/Build/vcvars64.bat"
	}
	return ""
}

// ArtifactPaths returns where a build writes its output. The name is tagged with
// a hash of the absolute source path so two files with the same stem in
// different directories never share an output.
func ArtifactPaths(file, buildDir string) (exe, jar, obj string) {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	sum := sha1.Sum([]byte(file))
	tag := base + "-" + hex.EncodeToString(sum[:])[:8]
	if buildDir == "" {
		buildDir = os.TempDir()
	}
	exeName := tag
	if platform.OSName("") == "windows" {
		exeName += ".exe"
	}
	return filepath.Join(buildDir, exeName),
		filepath.Join(buildDir, tag+".jar"),
		filepath.Join(buildDir, tag+".o")
}
