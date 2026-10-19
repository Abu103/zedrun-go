package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// userEnv points the user config dir at a temp XDG dir.
func userEnv(t *testing.T) (map[string]string, string) {
	t.Helper()
	home := t.TempDir()
	return env("XDG_CONFIG_HOME", home, "HOME", home), filepath.Join(home, "zedrun", ".zedrun.json")
}

func TestDefaults(t *testing.T) {
	e, _ := userEnv(t)
	cfg, warns := Load("", e, "linux")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !cfg.ShowFooter {
		t.Error("ShowFooter default = false, want true")
	}
	if cfg.ClearScreen {
		t.Error("ClearScreen default = true, want false")
	}
	if cfg.Timeout != 30 {
		t.Errorf("Timeout default = %v, want 30", cfg.Timeout)
	}
	if cfg.Prefer == nil || cfg.Paths == nil || cfg.Flags == nil {
		t.Error("maps not initialised")
	}
}

func TestUserConfigIsRead(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": 7, "showFooter": false, "prefer": {"c": "clang"}}`)
	cfg, warns := Load("", e, "linux")
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if cfg.Timeout != 7 || cfg.ShowFooter || cfg.Prefer["c"] != "clang" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestProjectOverridesUser(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": 5, "prefer": {"python": "python"}, "args": ["--user"]}`)
	proj := t.TempDir()
	write(t, filepath.Join(proj, ".zedrun.json"), `{"timeout": 9, "prefer": {"c": "clang"}}`)

	cfg, warns := Load(proj, e, "linux")
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if cfg.Timeout != 9 {
		t.Errorf("Timeout = %v, want 9 (project wins)", cfg.Timeout)
	}
	if cfg.Prefer["c"] != "clang" {
		t.Errorf("Prefer[c] = %q, want clang", cfg.Prefer["c"])
	}
	if cfg.Prefer["python"] != "python" {
		t.Errorf("Prefer[python] = %q, want python (user value kept)", cfg.Prefer["python"])
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "--user" {
		t.Errorf("Args = %v, want [--user]", cfg.Args)
	}
}

func TestProjectConfigFoundUpwards(t *testing.T) {
	e, _ := userEnv(t)
	root := t.TempDir()
	write(t, filepath.Join(root, ".zedrun.json"), `{"timeout": 11}`)
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := FindProjectConfig(deep); !ok || p != filepath.Join(root, ".zedrun.json") {
		t.Fatalf("FindProjectConfig = (%q,%v)", p, ok)
	}
	cfg, _ := Load(deep, e, "linux")
	if cfg.Timeout != 11 {
		t.Fatalf("Timeout = %v, want 11 from the ancestor config", cfg.Timeout)
	}
}

func TestCorruptConfigWarns(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": 7, "oops" `)
	cfg, warns := Load("", e, "linux")
	if len(warns) == 0 {
		t.Fatal("corrupt config produced no warning")
	}
	if cfg.Timeout != 30 {
		t.Fatalf("Timeout = %v, want the default 30", cfg.Timeout)
	}
	if !strings.Contains(strings.ToLower(strings.Join(warns, " ")), "corrupt") {
		t.Fatalf("warnings = %v, want one mentioning a corrupt config", warns)
	}
}

func TestUnknownKeyWarns(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": 3, "nonsense": 1}`)
	_, warns := Load("", e, "linux")
	if len(warns) == 0 {
		t.Fatal("unknown key produced no warning")
	}
	if !strings.Contains(strings.Join(warns, " "), "nonsense") {
		t.Fatalf("warnings = %v, want one naming the key", warns)
	}
}

func TestWrongTypeWarns(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": "soon", "prefer": 5}`)
	cfg, warns := Load("", e, "linux")
	if len(warns) < 2 {
		t.Fatalf("warnings = %v, want one per bad key", warns)
	}
	if cfg.Timeout != 30 {
		t.Fatalf("Timeout = %v, want default", cfg.Timeout)
	}
}

func TestBoolIsNotAValidNumber(t *testing.T) {
	e, userPath := userEnv(t)
	write(t, userPath, `{"timeout": true}`)
	cfg, warns := Load("", e, "linux")
	if len(warns) == 0 {
		t.Fatal("bool timeout accepted silently")
	}
	if cfg.Timeout != 30 {
		t.Fatalf("Timeout = %v, want default 30 (bool is not a number)", cfg.Timeout)
	}
}

func TestSetPreferenceCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".zedrun.json")
	msg, err := SetPreference(path, "c", "clang", "/usr/bin/clang")
	if err != nil {
		t.Fatalf("SetPreference: %v (%s)", err, msg)
	}
	cfg, warns := Load(dir, env("XDG_CONFIG_HOME", dir, "HOME", dir), "linux")
	if len(warns) != 0 {
		t.Fatalf("warnings after write: %v", warns)
	}
	if cfg.Prefer["c"] != "clang" {
		t.Fatalf("Prefer[c] = %q, want clang", cfg.Prefer["c"])
	}
	if cfg.Paths["clang"] != "/usr/bin/clang" {
		t.Fatalf("Paths[clang] = %q", cfg.Paths["clang"])
	}
}

func TestSetPreferenceRefusesCommentedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".zedrun.json")
	write(t, path, "{\n  // keep my comment\n  \"timeout\": 3\n}\n")
	if _, err := SetPreference(path, "c", "clang", ""); err == nil {
		t.Fatal("SetPreference rewrote a file containing comments")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "// keep my comment") {
		t.Fatal("comment was lost")
	}
}
