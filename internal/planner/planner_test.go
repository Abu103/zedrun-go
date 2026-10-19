package planner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

func env(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func fakeTool(t *testing.T, dir, name, version string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+name+" "+version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeLang() map[string]catalog.Language {
	return map[string]catalog.Language{
		"fake": {Key: "fake", Exts: []string{".fake"}, Runners: []catalog.Runner{
			{Tool: []string{"nosuchtool"}, Steps: [][]string{{"{tool}", "{file}"}}},
			{Tool: []string{"mytool"}, Steps: [][]string{{"{tool}", "{file}"}}},
		}},
	}
}

func TestExpandScalarsAndSplices(t *testing.T) {
	values := map[string]string{"tool": "/usr/bin/gcc", "file": "/p/a.c", "dir": "/p", "stem": "a"}
	splices := map[string][]string{"flags": {"-Wall"}}
	got := Expand([]string{"{tool}", "{file}", "{flags}", "{std}", "{args}"}, values, splices, nil)
	want := []string{"/usr/bin/gcc", "/p/a.c", "-Wall"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Expand = %v, want %v (empty splices must vanish)", got, want)
	}
}

func TestExpandSpliceKeepsAllParts(t *testing.T) {
	got := Expand([]string{"x", "{args}", "y"}, map[string]string{}, map[string][]string{"args": {"--a", "--b"}}, nil)
	want := []string{"x", "--a", "--b", "y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Expand = %v, want %v", got, want)
	}
}

func TestExpandToolLookup(t *testing.T) {
	lookup := func(id string) string { return "/path/to/" + id }
	got := Expand([]string{"{tool:javac}", "-cp", "{dir}"}, map[string]string{"dir": "/p"}, nil, lookup)
	want := []string{"/path/to/javac", "-cp", "/p"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Expand = %v, want %v", got, want)
	}
}

func TestCheckWhenConditions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := map[string]any{"os": "linux", "root": dir, "version": "1.79.0", "tools": map[string]bool{"cargo": true}}

	if !CheckWhen("project:Cargo.toml", ctx) {
		t.Error("project:Cargo.toml = false, want true")
	}
	if CheckWhen("project:go.mod", ctx) {
		t.Error("project:go.mod = true, want false")
	}
	if !CheckWhen("has:cargo", ctx) {
		t.Error("has:cargo = false, want true")
	}
	if CheckWhen("has:go", ctx) {
		t.Error("has:go = true, want false")
	}
	if !CheckWhen("version>=1.70", ctx) {
		t.Error("version>=1.70 = false, want true")
	}
	if CheckWhen("version>=2.0", ctx) {
		t.Error("version>=2.0 = true, want false")
	}
	if !CheckWhen("os:posix", ctx) {
		t.Error("os:posix = false, want true")
	}
	if CheckWhen("os:windows", ctx) {
		t.Error("os:windows = true, want false")
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "src", "bin")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindUp(deep, "Cargo.toml")
	if !ok || got != root {
		t.Fatalf("FindUp = (%q,%v), want (%q,true)", got, ok, root)
	}
	if _, ok := FindUp(deep, "nope.toml"); ok {
		t.Fatal("FindUp found a file that does not exist")
	}
}

func TestResolveFirstUsableRunner(t *testing.T) {
	dir := t.TempDir()
	p := fakeTool(t, dir, "mytool", "1.0.0")
	cfg := config.Default()
	cfg.Paths["mytool"] = p

	res, err := Resolve("fake", filepath.Join(dir, "a.fake"), cfg, nil, "", env(), "linux", fakeLang())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Install.Tool != "mytool" || res.Install.Path != p {
		t.Fatalf("resolved %+v, want mytool", res.Install)
	}
}

func TestPreferReorders(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, dir, "atool", "1.0.0")
	fakeTool(t, dir, "btool", "2.0.0")
	langs := map[string]catalog.Language{
		"fake": {Key: "fake", Exts: []string{".fake"}, Runners: []catalog.Runner{
			{Tool: []string{"atool"}, Steps: [][]string{{"{tool}"}}},
			{Tool: []string{"btool"}, Steps: [][]string{{"{tool}"}}},
		}},
	}
	cfg := config.Default()
	cfg.Paths["atool"] = filepath.Join(dir, "atool")
	cfg.Paths["btool"] = filepath.Join(dir, "btool")

	res, err := Resolve("fake", filepath.Join(dir, "a.fake"), cfg, nil, "", env(), "linux", langs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Install.Tool != "atool" {
		t.Fatalf("default choice = %q, want atool", res.Install.Tool)
	}

	cfg.Prefer["fake"] = "btool"
	res, err = Resolve("fake", filepath.Join(dir, "a.fake"), cfg, nil, "", env(), "linux", langs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Install.Tool != "btool" {
		t.Fatalf("preferred choice = %q, want btool", res.Install.Tool)
	}
}

func TestForceTool(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths["atool"] = fakeTool(t, dir, "atool", "1.0.0")
	cfg.Paths["btool"] = fakeTool(t, dir, "btool", "2.0.0")
	langs := map[string]catalog.Language{
		"fake": {Key: "fake", Exts: []string{".fake"}, Runners: []catalog.Runner{
			{Tool: []string{"atool"}, Steps: [][]string{{"{tool}"}}},
			{Tool: []string{"btool"}, Steps: [][]string{{"{tool}"}}},
		}},
	}
	res, err := Resolve("fake", filepath.Join(dir, "a.fake"), cfg, nil, "btool", env(), "linux", langs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Install.Tool != "btool" {
		t.Fatalf("forced choice = %q, want btool", res.Install.Tool)
	}
}

func TestNoToolchainListsTried(t *testing.T) {
	cfg := config.Default()
	_, err := Resolve("fake", "/tmp/a.fake", cfg, nil, "", env(), "linux", fakeLang())
	if !errors.Is(err, ErrNoToolchain) {
		t.Fatalf("err = %v, want ErrNoToolchain", err)
	}
	if !strings.Contains(err.Error(), "nosuchtool") {
		t.Fatalf("error %q does not list the tools it tried", err)
	}
}

func cResolution(t *testing.T) Resolution {
	t.Helper()
	dir := t.TempDir()
	return Resolution{
		Language: "c",
		Runner:   catalog.Languages["c"].Runners[0],
		Install:  detect.Install{Tool: "gcc", Path: "/usr/bin/gcc", Version: "13.3.0", OK: true},
		Root:     dir,
		ToolPaths: map[string]string{
			"gcc": "/usr/bin/gcc",
		},
	}
}

func TestPlanCGccFlagsStdLibsRelease(t *testing.T) {
	res := cResolution(t)
	build := t.TempDir()
	plan := BuildPlan(filepath.Join(res.Root, "main.c"), res, config.Default(), false, []string{"--x"}, build)

	first := strings.Join(plan.Steps[0].Argv, " ")
	if !strings.Contains(first, "-std=c17") {
		t.Errorf("step %q missing the std flag", first)
	}
	if !strings.Contains(first, "-lm") {
		t.Errorf("step %q missing the libs", first)
	}
	if !strings.HasSuffix(plan.Steps[len(plan.Steps)-1].Argv[len(plan.Steps[len(plan.Steps)-1].Argv)-1], "--x") {
		t.Errorf("args not appended: %v", plan.Steps)
	}

	rel := BuildPlan(filepath.Join(res.Root, "main.c"), res, config.Default(), true, nil, build)
	if !strings.Contains(strings.Join(rel.Steps[0].Argv, " "), "-O2") {
		t.Errorf("release plan %v missing -O2", rel.Steps[0].Argv)
	}

	cfg := config.Default()
	cfg.Std["c"] = "-std=c11"
	cfg.Flags["c"] = []string{"-Wall"}
	over := BuildPlan(filepath.Join(res.Root, "main.c"), res, cfg, false, nil, build)
	joined := strings.Join(over.Steps[0].Argv, " ")
	if !strings.Contains(joined, "-std=c11") || strings.Contains(joined, "-std=c17") {
		t.Errorf("std override not applied: %q", joined)
	}
	if !strings.Contains(joined, "-Wall") {
		t.Errorf("flags not applied: %q", joined)
	}
}

func TestPlanWritesExact(t *testing.T) {
	res := cResolution(t)
	build := t.TempDir()
	plan := BuildPlan(filepath.Join(res.Root, "main.c"), res, config.Default(), false, nil, build)
	exe, _, _ := ArtifactPaths(filepath.Join(res.Root, "main.c"), build)
	if len(plan.Writes) != 1 || plan.Writes[0] != exe {
		t.Fatalf("Writes = %v, want exactly [%s]", plan.Writes, exe)
	}
}

func TestPlanPrependsToolDirToPath(t *testing.T) {
	res := cResolution(t)
	plan := BuildPlan(filepath.Join(res.Root, "main.c"), res, config.Default(), false, nil, t.TempDir())
	if len(plan.PathPrefix) != 1 || plan.PathPrefix[0] != filepath.Dir(res.Install.Path) {
		t.Fatalf("PathPrefix = %v, want [%s]", plan.PathPrefix, filepath.Dir(res.Install.Path))
	}
}

func TestPlanCargoProjectUsesRootCwd(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(proj, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	res := Resolution{
		Language:  "rust",
		Runner:    catalog.Languages["rust"].Runners[0],
		Install:   detect.Install{Tool: "cargo", Path: "/usr/bin/cargo", OK: true},
		Root:      proj,
		ToolPaths: map[string]string{"cargo": "/usr/bin/cargo"},
	}
	plan := BuildPlan(filepath.Join(src, "main.rs"), res, config.Default(), false, nil, t.TempDir())
	if plan.Steps[0].Cwd != proj {
		t.Fatalf("cwd = %q, want the project root %q", plan.Steps[0].Cwd, proj)
	}
}

func TestPlanMSVCWritesBat(t *testing.T) {
	res := Resolution{
		Language:  "c",
		Runner:    catalog.Languages["c"].Runners[1],
		Install:   detect.Install{Tool: "msvc", Path: `C:\VS\VC\Tools\MSVC\14.3\bin\Hostx64\x64\cl.exe`, OK: true},
		Root:      t.TempDir(),
		ToolPaths: map[string]string{"msvc": `C:\VS\VC\Tools\MSVC\14.3\bin\Hostx64\x64\cl.exe`},
	}
	plan := BuildPlan(filepath.Join(res.Root, "main.c"), res, config.Default(), false, nil, t.TempDir())

	var bat string
	for _, w := range plan.Writes {
		if strings.HasSuffix(strings.ToLower(w), ".bat") {
			bat = w
		}
	}
	if bat == "" {
		t.Fatalf("no .bat in Writes: %v", plan.Writes)
	}
	content, ok := plan.Scripts[bat]
	if !ok {
		t.Fatalf("no script content for %s", bat)
	}
	if !strings.Contains(content, "vcvars64.bat") {
		t.Fatalf("bat does not call vcvars64.bat:\n%s", content)
	}
	if !strings.HasPrefix(strings.ToLower(plan.Steps[0].Argv[0]), "cmd") {
		t.Fatalf("msvc plan does not run through cmd: %v", plan.Steps[0].Argv)
	}
}

func TestArtifactPathsUniquePerSource(t *testing.T) {
	build := t.TempDir()
	e1, j1, o1 := ArtifactPaths("/a/main.c", build)
	e2, j2, o2 := ArtifactPaths("/b/main.c", build)
	if e1 == e2 || j1 == j2 || o1 == o2 {
		t.Fatalf("same-stem files in different dirs collide: %s vs %s", e1, e2)
	}
	if filepath.Dir(e1) != build {
		t.Fatalf("artifact %s is not under the build dir %s", e1, build)
	}
}
