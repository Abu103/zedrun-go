// Package doctor reports what zedrun-go found on this machine: system paths,
// every install of every tool, the default toolchain per language, and the
// problems worth fixing. An absent tool is simply absent — it is not a problem.
package doctor

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/planner"
	"github.com/zedrun/zedrun-go/internal/platform"
)

// DefaultTools lists every tool id zedrun-go knows about, sorted.
func DefaultTools() []string {
	out := make([]string, 0, len(catalog.Tools))
	for id := range catalog.Tools {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Collect gathers the report. tools limits which tools are probed; nil means all.
func Collect(cfg config.Config, cache *detect.ProbeCache, env map[string]string, osOverride string, tools []string) map[string]any {
	if tools == nil {
		tools = DefaultTools()
	}
	report := map[string]any{
		"system": map[string]any{
			"os":         platform.OSName(osOverride),
			"go":         runtime.Version(),
			"config_dir": platform.ConfigDir(env, osOverride),
			"cache_dir":  platform.CacheDir(env, osOverride),
			"data_dir":   platform.DataDir(env, osOverride),
			"zed_dir":    platform.ZedConfigDir(env, osOverride),
		},
	}

	// Initialised, not nil: an empty report must marshal as [] rather than null,
	// so a JSON consumer can always index it.
	problems := []string{}
	toolRows := []any{}
	for _, id := range tools {
		installs := detect.FindInstalls(catalog.GetTool(id), cfg, cache, true, env, osOverride)
		usable := 0
		for _, in := range installs {
			if in.OK {
				usable++
			}
			toolRows = append(toolRows, map[string]any{
				"tool":    in.Tool,
				"version": in.Version,
				"origin":  in.Origin,
				"path":    in.Path,
				"ok":      in.OK,
				"note":    in.Note,
			})
		}
		if usable > 1 {
			// Several installs of one tool share the same tool id, so
			// `use <lang> <tool>` cannot tell them apart — it writes a
			// preference by tool id, which is the thing that collides. The path
			// is the only unambiguous handle, so point at that.
			problems = append(problems, fmt.Sprintf("%s: %d usable installs — pin one with `zedrun-go use %s <tool> --path <path>`", id, usable, id))
		}
		if len(installs) > 0 && usable == 0 {
			note := installs[0].Note
			if note == "" {
				note = "not usable"
			}
			problems = append(problems, fmt.Sprintf("%s: found at %s but %s", id, installs[0].Path, note))
		}
	}
	report["tools"] = toolRows

	langKeys := make([]string, 0, len(catalog.Languages))
	for k := range catalog.Languages {
		langKeys = append(langKeys, k)
	}
	sort.Strings(langKeys)
	langRows := []any{}
	for _, k := range langKeys {
		langRows = append(langRows, map[string]any{
			"language": k,
			"default":  defaultTool(k, cfg, cache, env, osOverride),
		})
	}
	report["languages"] = langRows
	report["problems"] = problems
	return report
}

func defaultTool(language string, cfg config.Config, cache *detect.ProbeCache, env map[string]string, osOverride string) string {
	res, err := planner.Resolve(language, "", cfg, cache, "", env, osOverride, nil)
	if err != nil {
		return "-"
	}
	return res.Install.Tool
}

// Render prints the report as plain text.
func Render(report map[string]any, out io.Writer, showAll bool) {
	if sys, ok := report["system"].(map[string]any); ok {
		fmt.Fprintln(out, "System")
		for _, k := range []string{"os", "go", "config_dir", "cache_dir", "data_dir", "zed_dir"} {
			fmt.Fprintf(out, "  %-11s %v\n", k, sys[k])
		}
	}

	tools := rows(report["tools"])
	if showAll || len(tools) > 0 {
		fmt.Fprintln(out, "\nTools")
		table(out, []string{"Tool", "Version", "Origin", "Path"},
			func() [][]string {
				var rs [][]string
				for _, r := range tools {
					path := str(r["path"])
					if note := str(r["note"]); note != "" {
						path += " (" + note + ")"
					}
					rs = append(rs, []string{str(r["tool"]), or(str(r["version"]), "?"), str(r["origin"]), path})
				}
				return rs
			}())
	}

	fmt.Fprintln(out, "\nLanguages (default tool)")
	table(out, []string{"Language", "Default"}, func() [][]string {
		var rs [][]string
		for _, r := range rows(report["languages"]) {
			rs = append(rs, []string{str(r["language"]), str(r["default"])})
		}
		return rs
	}())

	problems, _ := report["problems"].([]string)
	fmt.Fprintln(out)
	if len(problems) == 0 {
		fmt.Fprintln(out, "No problems detected.")
		return
	}
	fmt.Fprintln(out, "Problems")
	for _, p := range problems {
		fmt.Fprintf(out, "  - %s\n", p)
	}
}

func rows(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// table prints aligned columns.
func table(out io.Writer, headers []string, rs [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rs {
		for i := range headers {
			if i < len(r) && len(r[i]) > widths[i] {
				widths[i] = len(r[i])
			}
		}
	}
	printRow := func(cells []string) {
		parts := make([]string, len(headers))
		for i := range headers {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			if i == len(headers)-1 {
				parts[i] = c
			} else {
				parts[i] = fmt.Sprintf("%-*s", widths[i]+2, c)
			}
		}
		fmt.Fprintln(out, strings.TrimRight(strings.Join(parts, ""), " "))
	}
	printRow(headers)
	sep := make([]string, len(headers))
	for i := range headers {
		sep[i] = strings.Repeat("-", widths[i]+2)
	}
	printRow(sep)
	for _, r := range rs {
		printRow(r)
	}
}
