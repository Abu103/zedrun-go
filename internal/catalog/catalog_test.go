package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func placeholdersIn(arg string) []string {
	var out []string
	for {
		i := strings.IndexByte(arg, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(arg[i:], '}')
		if j < 0 {
			return out
		}
		out = append(out, arg[i+1:i+j])
		arg = arg[i+j+1:]
	}
}

func TestNoExtensionClaimedTwice(t *testing.T) {
	seen := map[string]string{}
	for key, l := range Languages {
		for _, e := range l.Exts {
			if prev, ok := seen[e]; ok {
				t.Errorf("extension %q claimed by both %s and %s", e, prev, key)
			}
			seen[e] = key
		}
	}
}

func TestEveryLanguageHasRunner(t *testing.T) {
	for key, l := range Languages {
		if len(l.Runners) == 0 {
			t.Errorf("language %s has no runners", key)
		}
		if len(l.Exts) == 0 && len(l.Shebangs) == 0 {
			t.Errorf("language %s has no extensions or shebangs", key)
		}
	}
}

func TestEveryRunnerToolKnown(t *testing.T) {
	for key, l := range Languages {
		for _, r := range l.Runners {
			if len(r.Tool) == 0 {
				t.Errorf("language %s has a runner with no tool", key)
			}
			for _, id := range r.Tool {
				if _, ok := Tools[id]; !ok {
					t.Errorf("language %s runner uses unknown tool %q", key, id)
				}
			}
			if len(r.Steps) == 0 {
				t.Errorf("language %s runner %v has no steps", key, r.Tool)
			}
		}
	}
}

func TestPlaceholdersKnown(t *testing.T) {
	for key, l := range Languages {
		for _, r := range l.Runners {
			for _, step := range r.Steps {
				for _, arg := range step {
					for _, ph := range placeholdersIn(arg) {
						if strings.HasPrefix(ph, "tool:") {
							if _, ok := Tools[ph[len("tool:"):]]; !ok {
								t.Errorf("language %s uses {tool:%s} which is not a known tool", key, ph[5:])
							}
							continue
						}
						if !KnownPlaceholders[ph] {
							t.Errorf("language %s step %v uses unknown placeholder {%s}", key, step, ph)
						}
					}
				}
			}
		}
	}
}

func TestLanguageForPathByExtension(t *testing.T) {
	cases := map[string]string{
		"a.py": "python", "a.c": "c", "a.cpp": "cpp", "a.cc": "cpp", "a.rs": "rust",
		"a.ts": "typescript", "a.mts": "typescript", "a.go": "go", "A.PY": "python",
		"a.sh": "shell", "a.sql": "sql", "a.kt": "kotlin",
	}
	for path, want := range cases {
		got, ok := LanguageForPath(path, nil)
		if !ok || got != want {
			t.Errorf("LanguageForPath(%q) = (%q,%v), want (%q,true)", path, got, ok, want)
		}
	}
	if _, ok := LanguageForPath("a.unknownext", nil); ok {
		t.Error("LanguageForPath(a.unknownext) matched, want no match")
	}
}

func TestLanguageForPathByShebang(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script")
	if err := os.WriteFile(p, []byte("#!/usr/bin/env python3\nprint(1)\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := LanguageForPath(p, nil)
	if !ok || got != "python" {
		t.Fatalf("shebang detection = (%q,%v), want (python,true)", got, ok)
	}
}

func TestBuildCustom(t *testing.T) {
	l := BuildCustom("mylang", map[string]any{
		"exts": []any{".ml", "mli"},
		"run":  []any{"mytool --run {file}"},
	})
	if l.Key != "mylang" {
		t.Errorf("Key = %q", l.Key)
	}
	if len(l.Exts) != 2 || l.Exts[0] != ".ml" || l.Exts[1] != ".mli" {
		t.Errorf("Exts = %v, want [.ml .mli]", l.Exts)
	}
	if len(l.Runners) != 1 || len(l.Runners[0].Steps) != 1 {
		t.Fatalf("Runners = %#v", l.Runners)
	}
	step := l.Runners[0].Steps[0]
	if len(step) != 3 || step[0] != "mytool" || step[2] != "{file}" {
		t.Errorf("step = %v, want [mytool --run {file}]", step)
	}
}

func TestBuildCustomQuotedArg(t *testing.T) {
	l := BuildCustom("q", map[string]any{"exts": []any{".q"}, "run": []any{`mytool --msg "hello world" {file}`}})
	step := l.Runners[0].Steps[0]
	if len(step) != 4 || step[2] != "hello world" {
		t.Fatalf("step = %v, want quoted arg kept whole", step)
	}
}

func TestInstallHintFallback(t *testing.T) {
	h := InstallHint("nosuchtool", "linux")
	if !strings.Contains(h, "nosuchtool") {
		t.Fatalf("InstallHint fallback = %q, want it to name the tool", h)
	}
	if InstallHint("gcc", "linux") == "" {
		t.Fatal("InstallHint(gcc, linux) is empty")
	}
}

func TestGetToolUnknownIsAdHoc(t *testing.T) {
	tool := GetTool("nosuchtool")
	if tool.ID != "nosuchtool" || len(tool.Names) == 0 || len(tool.VersionArgs) == 0 {
		t.Fatalf("GetTool ad-hoc = %#v", tool)
	}
}

func TestTypeScriptHasTscNodeFallback(t *testing.T) {
	ts, ok := Languages["typescript"]
	if !ok {
		t.Fatal("no typescript language")
	}
	for _, r := range ts.Runners {
		if len(r.Tool) == 2 && r.Tool[0] == "tsc" && r.Tool[1] == "node" {
			return
		}
	}
	t.Fatal("typescript has no tsc+node fallback runner")
}

func TestEveryLanguageKeyMatchesItsMapKey(t *testing.T) {
	for key, l := range Languages {
		if l.Key != key {
			t.Errorf("Languages[%q].Key = %q", key, l.Key)
		}
	}
}
