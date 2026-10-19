// Package config loads zedrun-go configuration from the user config dir and a
// project .zedrun.json, validates it, and merges project over user. A corrupt
// or mistyped file warns and falls back to defaults — it never aborts a run.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zedrun/zedrun-go/internal/jsonc"
	"github.com/zedrun/zedrun-go/internal/platform"
)

// FileName is the config file name, used both in the config dir and per project.
const FileName = ".zedrun.json"

// Config is the merged configuration.
type Config struct {
	Prefer       map[string]string   // language -> tool id to try first
	Paths        map[string]string   // tool id -> pinned executable path
	ExtraDirs    []string            // extra directories to search
	Flags        map[string][]string // language -> extra compiler/interpreter flags
	Std          map[string]string   // language -> {std} value
	Args         []string            // extra arguments passed to the program
	Env          map[string]string   // environment overrides for the run
	Languages    map[string]any      // custom or overridden language specs
	Rules        map[string]string   // glob -> command template, overrides the language
	ClearScreen  bool
	ShowFooter   bool
	EchoCommands bool
	Timeout      float64 // seconds; 0 means no timeout
	Sources      map[string]any
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Prefer:     map[string]string{},
		Paths:      map[string]string{},
		Flags:      map[string][]string{},
		Std:        map[string]string{},
		Env:        map[string]string{},
		Languages:  map[string]any{},
		Rules:      map[string]string{},
		Sources:    map[string]any{},
		ShowFooter: true,
		Timeout:    30,
	}
}

// UserConfigPath is the user-level config file location.
func UserConfigPath(env map[string]string, osOverride string) string {
	return filepath.Join(platform.ConfigDir(env, osOverride), FileName)
}

// FindProjectConfig walks up from start looking for a project config file.
func FindProjectConfig(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir := start
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		p := filepath.Join(dir, FileName)
		if fileExists(p) {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Load reads the user config and, when projectStart is set, the nearest project
// config, applying project values on top of user values. Warnings describe
// anything skipped.
func Load(projectStart string, env map[string]string, osOverride string) (Config, []string) {
	cfg := Default()
	var warns []string
	if p := UserConfigPath(env, osOverride); fileExists(p) {
		applyFile(&cfg, p, &warns)
	}
	if p, ok := FindProjectConfig(projectStart); ok {
		applyFile(&cfg, p, &warns)
	}
	return cfg, warns
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func applyFile(cfg *Config, path string, warns *[]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		*warns = append(*warns, fmt.Sprintf("cannot read %s: %v", path, err))
		return
	}
	v, err := jsonc.Loads(string(data))
	if err != nil {
		*warns = append(*warns, fmt.Sprintf("corrupt config %s: %v — using defaults", path, err))
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		*warns = append(*warns, fmt.Sprintf("corrupt config %s: top level is not an object — using defaults", path))
		return
	}
	applyMap(cfg, m, path, warns)
}

func applyMap(cfg *Config, m map[string]any, path string, warns *[]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		switch k {
		case "prefer":
			if mm, ok := asStringMap(v); ok {
				for kk, vv := range mm {
					cfg.Prefer[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of strings")
			}
		case "paths":
			if mm, ok := asStringMap(v); ok {
				for kk, vv := range mm {
					cfg.Paths[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of strings")
			}
		case "extraDirs":
			if ss, ok := asStrings(v); ok {
				cfg.ExtraDirs = append(cfg.ExtraDirs, ss...)
			} else {
				bad(warns, path, k, "a list of strings")
			}
		case "flags":
			if mm, ok := asStringListMap(v); ok {
				for kk, vv := range mm {
					cfg.Flags[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of string lists")
			}
		case "std":
			if mm, ok := asStringMap(v); ok {
				for kk, vv := range mm {
					cfg.Std[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of strings")
			}
		case "args":
			if ss, ok := asStrings(v); ok {
				cfg.Args = ss
			} else {
				bad(warns, path, k, "a list of strings")
			}
		case "env":
			if mm, ok := asStringMap(v); ok {
				for kk, vv := range mm {
					cfg.Env[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of strings")
			}
		case "languages":
			if mm, ok := v.(map[string]any); ok {
				for kk, vv := range mm {
					cfg.Languages[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object")
			}
		case "rules":
			if mm, ok := asStringMap(v); ok {
				for kk, vv := range mm {
					cfg.Rules[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object of strings")
			}
		case "clearScreen":
			if b, ok := v.(bool); ok {
				cfg.ClearScreen = b
			} else {
				bad(warns, path, k, "a boolean")
			}
		case "showFooter":
			if b, ok := v.(bool); ok {
				cfg.ShowFooter = b
			} else {
				bad(warns, path, k, "a boolean")
			}
		case "echoCommands":
			if b, ok := v.(bool); ok {
				cfg.EchoCommands = b
			} else {
				bad(warns, path, k, "a boolean")
			}
		case "timeout":
			if n, ok := asNumber(v); ok {
				cfg.Timeout = n
			} else {
				bad(warns, path, k, "a number of seconds")
			}
		case "sources":
			if mm, ok := v.(map[string]any); ok {
				for kk, vv := range mm {
					cfg.Sources[kk] = vv
				}
			} else {
				bad(warns, path, k, "an object")
			}
		default:
			*warns = append(*warns, fmt.Sprintf("%s: unknown key %q", path, k))
		}
	}
}

func bad(warns *[]string, path, key, want string) {
	*warns = append(*warns, fmt.Sprintf("%s: %q must be %s", path, key, want))
}

func asStringMap(v any) (map[string]string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[k] = s
	}
	return out, true
}

func asStrings(v any) ([]string, bool) {
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(a))
	for _, e := range a {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func asStringListMap(v any) (map[string][]string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string][]string, len(m))
	for k, e := range m {
		ss, ok := asStrings(e)
		if !ok {
			return nil, false
		}
		out[k] = ss
	}
	return out, true
}

// asNumber rejects booleans: in Go, true would otherwise convert silently.
func asNumber(v any) (float64, bool) {
	if _, isBool := v.(bool); isBool {
		return 0, false
	}
	n, ok := v.(float64)
	return n, ok
}

// SetPreference records a language's preferred tool (and optionally its path)
// in a config file. It refuses a file that contains comments rather than
// rewriting it and losing them.
func SetPreference(path, language, toolID, toolPath string) (string, error) {
	m := map[string]any{}
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if jsonc.HasComments(string(data)) {
			return "", fmt.Errorf("%s contains comments; edit it by hand to keep them", path)
		}
		if v, err := jsonc.Loads(string(data)); err == nil {
			if mm, ok := v.(map[string]any); ok {
				m = mm
			}
		}
	}
	prefer, _ := m["prefer"].(map[string]any)
	if prefer == nil {
		prefer = map[string]any{}
	}
	prefer[language] = toolID
	m["prefer"] = prefer
	if toolPath != "" {
		paths, _ := m["paths"].(map[string]any)
		if paths == nil {
			paths = map[string]any{}
		}
		paths[toolID] = toolPath
		m["paths"] = paths
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s", path), nil
}
