package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/config"
)

func env(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func fakeExe(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func problemsOf(t *testing.T, report map[string]any) []string {
	t.Helper()
	raw, ok := report["problems"].([]string)
	if !ok {
		t.Fatalf("problems is %T, want []string", report["problems"])
	}
	return raw
}

func anyContains(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func TestCollectFlagsMultipleInstalls(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	fakeExe(t, dirA, "mytool", "echo 'mytool 1.0.0'")
	fakeExe(t, dirB, "mytool", "echo 'mytool 2.0.0'")

	report := Collect(config.Default(), nil, env("PATH", dirA+":"+dirB), "linux", []string{"mytool"})
	if !anyContains(problemsOf(t, report), "2 usable installs") {
		t.Fatalf("problems = %v, want a multiple-install note", problemsOf(t, report))
	}
}

func TestCollectFlagsUnusable(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "badtool", "exit 1")

	report := Collect(config.Default(), nil, env("PATH", dir), "linux", []string{"badtool"})
	if !anyContains(problemsOf(t, report), "not usable") {
		t.Fatalf("problems = %v, want an unusable-install note", problemsOf(t, report))
	}
}

func TestMissingInstallNotedButNotAProblem(t *testing.T) {
	report := Collect(config.Default(), nil, env("PATH", t.TempDir()), "linux", []string{"nosuchtool"})
	if probs := problemsOf(t, report); len(probs) != 0 {
		t.Fatalf("problems = %v, want none for an absent tool", probs)
	}
	rows, ok := report["tools"].([]any)
	if !ok {
		t.Fatalf("tools is %T, want a list", report["tools"])
	}
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok && m["tool"] == "nosuchtool" {
			t.Fatalf("absent tool listed as an install: %+v", m)
		}
	}
}

func TestCollectIsJSONSerialisable(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "mytool", "echo 'mytool 1.0.0'")
	report := Collect(config.Default(), nil, env("PATH", dir), "linux", []string{"mytool", "nosuchtool"})
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("report is not JSON-serialisable: %v", err)
	}
}

func TestProblemsMarshalAsEmptySlice(t *testing.T) {
	home := t.TempDir()
	e := env("HOME", home, "XDG_CONFIG_HOME", filepath.Join(home, ".config"),
		"XDG_CACHE_HOME", filepath.Join(home, ".cache"),
		"XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	d := Collect(config.Default(), nil, e, "linux", nil)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"problems":[]`) {
		t.Fatalf("an empty report must still marshal problems as [], got: %s", b)
	}
}

func TestCollectSystemSection(t *testing.T) {
	report := Collect(config.Default(), nil, env("HOME", "/home/z", "XDG_CONFIG_HOME", "/home/z/.config"), "linux", nil)
	sys, ok := report["system"].(map[string]any)
	if !ok {
		t.Fatalf("system is %T", report["system"])
	}
	if sys["os"] != "linux" {
		t.Errorf("system.os = %v, want linux", sys["os"])
	}
	if sys["config_dir"] != "/home/z/.config/zedrun" {
		t.Errorf("system.config_dir = %v", sys["config_dir"])
	}
	if sys["zed_dir"] != "/home/z/.config/zed" {
		t.Errorf("system.zed_dir = %v", sys["zed_dir"])
	}
}

func TestRenderPrintsSections(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "mytool", "echo 'mytool 1.0.0'")
	report := Collect(config.Default(), nil, env("PATH", dir), "linux", []string{"mytool"})

	var buf bytes.Buffer
	Render(report, &buf, false)
	out := buf.String()
	for _, want := range []string{"System", "Tools", "mytool", "1.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "No problems detected") {
		t.Errorf("render output missing the all-clear line:\n%s", out)
	}
}

func TestRenderShowsProblems(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "badtool", "exit 1")
	report := Collect(config.Default(), nil, env("PATH", dir), "linux", []string{"badtool"})

	var buf bytes.Buffer
	Render(report, &buf, false)
	if !strings.Contains(buf.String(), "not usable") {
		t.Fatalf("render did not show the problem:\n%s", buf.String())
	}
}

func TestDefaultToolsCoversCatalog(t *testing.T) {
	tools := DefaultTools()
	if len(tools) < 20 {
		t.Fatalf("DefaultTools returned %d tools, want the full catalog", len(tools))
	}
	for i := 1; i < len(tools); i++ {
		if tools[i-1] >= tools[i] {
			t.Fatalf("DefaultTools is not sorted: %v", tools)
		}
	}
}
