package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/provision"
)

func inst(path string, ok bool) detect.Install {
	return detect.Install{Tool: "go", Path: path, Version: "1.24.0", OK: ok}
}

// Review focus: a legacy .bat shim and a real .exe on a Windows PATH. The repair
// must surface the real executable's directory, not just the tool id, in both
// orders.
func TestShimShadowedByRealExe(t *testing.T) {
	real := `C:\tools\go\bin\go.exe`
	shim := `C:\old\shims\go.bat`
	env := map[string]string{"PATH": `C:\tools\go\bin;C:\Windows;C:\old\shims`}
	st := Assemble(Inputs{Tool: "go", OS: "windows", Env: env, Installs: []detect.Install{inst(shim, true), inst(real, true)}})

	if st.Active == nil || st.Active.Path != real {
		t.Fatalf("active = %+v, want the real exe %s (earlier PATH entry)", st.Active, real)
	}
	if len(st.Shadowed) != 1 || st.Shadowed[0].Loser.Path != shim || !st.Shadowed[0].Loser.Shim {
		t.Fatalf("shadowed = %+v, want the .bat shim hidden behind the exe", st.Shadowed)
	}
	if d := Diagnose(st, Options{}); d.Kind != KindOK {
		t.Fatalf("real exe wins PATH, diagnosis = %s, want ok", d.Kind)
	}
}

func TestShimWinsPathSurfacesRealDirectory(t *testing.T) {
	real := `C:\tools\go\bin\go.exe`
	shim := `C:\old\shims\go.bat`
	env := map[string]string{"PATH": `C:\old\shims;C:\Windows;C:\tools\go\bin`}
	st := Assemble(Inputs{Tool: "go", OS: "windows", Env: env, Installs: []detect.Install{inst(shim, true), inst(real, true)}})

	d := Diagnose(st, Options{})
	if d.Kind != KindShimmed {
		t.Fatalf("kind = %s, want shimmed", d.Kind)
	}
	if d.Dir != `C:\tools\go\bin` {
		t.Fatalf("dir = %q, want the real exe's directory", d.Dir)
	}
	p := Repair(st, d, Options{Shell: ShellPowerShell})
	var joined strings.Builder
	for _, s := range p.Steps {
		joined.WriteString(s.Command + "\n")
	}
	if !strings.Contains(joined.String(), `C:\tools\go\bin`) {
		t.Fatalf("repair never names the directory:\n%s", joined.String())
	}
	if strings.Contains(joined.String(), `C:\old\shims`) {
		t.Fatalf("repair points at the shim directory:\n%s", joined.String())
	}
}

func TestPathIndexIsCaseAndSeparatorInsensitiveOnWindows(t *testing.T) {
	env := map[string]string{"PATH": `c:\TOOLS\go\bin\`}
	st := Assemble(Inputs{Tool: "go", OS: "windows", Env: env, Installs: []detect.Install{inst(`C:\tools\go\bin\go.exe`, true)}})
	if st.Active == nil || !st.Installs[0].OnPath {
		t.Fatalf("a case or trailing-slash difference hid an install on PATH: %+v", st.Installs)
	}
}

func TestUnusableInstallsNeverBecomeActive(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	st := Assemble(Inputs{Tool: "go", OS: "linux", Env: env, Installs: []detect.Install{inst("/usr/bin/go", false)}})
	if st.Present || st.Active != nil {
		t.Fatalf("an install that failed its probe counted as present: %+v", st)
	}
}

func TestOffPathInstall(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	st := Assemble(Inputs{Tool: "go", OS: "linux", Env: env, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	if !st.Present || st.Active != nil || len(st.OffPath) != 1 {
		t.Fatalf("want present, no active, one off-PATH install: %+v", st)
	}
	d := Diagnose(st, Options{})
	if d.Kind != KindNotOnPath || d.Dir != "/opt/go/bin" {
		t.Fatalf("diagnosis = %+v", d)
	}
}

func TestStalePathWhenProfileAlreadyHasTheDirectory(t *testing.T) {
	home := "/home/u"
	env := map[string]string{"PATH": "/usr/bin", "HOME": home}
	st := Assemble(Inputs{Tool: "go", OS: "linux", Shell: ShellBash, Env: env, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	read := func(p string) ([]byte, error) {
		if p == filepath.Join(home, ".bashrc") {
			return []byte("# mine\nexport PATH=/opt/go/bin:\"$PATH\"\n"), nil
		}
		return nil, os.ErrNotExist
	}
	d := Diagnose(st, Options{ReadFile: read})
	if d.Kind != KindStalePath {
		t.Fatalf("kind = %s, want stale-path", d.Kind)
	}
	p := Repair(st, d, Options{ReadFile: read})
	if len(p.Steps) != 1 || p.Steps[0].Command != "source ~/.bashrc" {
		t.Fatalf("stale PATH should only reload, got %+v", p.Steps)
	}
}

func TestManagerMissing(t *testing.T) {
	routes := []provision.Method{{Tool: "go", Manager: "winget", Argv: []string{"winget", "install", "GoLang.Go"}, Present: false}}
	st := Assemble(Inputs{Tool: "go", OS: "windows", Env: map[string]string{"PATH": `C:\Windows`}, Routes: routes})
	d := Diagnose(st, Options{})
	if d.Kind != KindManagerMissing {
		t.Fatalf("kind = %s, want manager-missing", d.Kind)
	}
	p := Repair(st, d, Options{})
	if p.Shape != ShapeBlock || p.Steps[0].Command != "https://aka.ms/getwinget" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestNoRoute(t *testing.T) {
	st := Assemble(Inputs{Tool: "go", OS: "linux", Env: map[string]string{"PATH": "/usr/bin"}})
	if d := Diagnose(st, Options{}); d.Kind != KindNoRoute {
		t.Fatalf("kind = %s, want no-route", d.Kind)
	}
}

// Review focus: elevation. With `sudo -n` failing, the method is explained, not
// run, and the plan carries the exact command for the user to type.
func TestElevationNoSudoReturnsExactUserCommand(t *testing.T) {
	old := provision.SudoProbe
	provision.SudoProbe = func(map[string]string) bool { return false }
	defer func() { provision.SudoProbe = old }()

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": bin}
	routes := []provision.Method{{Tool: "gcc", Manager: "apt", Package: "gcc",
		Argv: []string{"apt-get", "install", "-y", "gcc"}, Elevated: true, Present: true}}
	st := Assemble(Inputs{Tool: "gcc", OS: "linux", Env: env, Routes: routes})

	if st.Elevation.CanRun || !st.Elevation.Needed {
		t.Fatalf("elevation = %+v, want needed and not runnable", st.Elevation)
	}
	d := Diagnose(st, Options{})
	if d.Kind != KindNoSudo {
		t.Fatalf("kind = %s, want no-sudo", d.Kind)
	}
	p := Repair(st, d, Options{})
	if p.CanRun {
		t.Fatal("plan says zedrun-go can run an elevated method with no passwordless sudo")
	}
	if len(p.Steps) != 1 || !p.Steps[0].Explain || p.Steps[0].Command != "sudo apt-get install -y gcc" {
		t.Fatalf("steps = %+v, want one explained step with the exact sudo command", p.Steps)
	}
	if p.Stop == "" {
		t.Fatal("a step that needs a password must stop the plan")
	}
}

func TestElevationRunsThroughPasswordlessSudo(t *testing.T) {
	old := provision.SudoProbe
	provision.SudoProbe = func(map[string]string) bool { return true }
	defer func() { provision.SudoProbe = old }()
	routes := []provision.Method{{Tool: "gcc", Manager: "apt", Argv: []string{"apt-get", "install", "-y", "gcc"}, Elevated: true, Present: true}}
	st := Assemble(Inputs{Tool: "gcc", OS: "linux", Env: map[string]string{"PATH": "/usr/bin"}, Routes: routes})
	if !st.Elevation.Sudo || !st.Elevation.CanRun {
		t.Fatalf("elevation = %+v, want sudo and runnable", st.Elevation)
	}
	if d := Diagnose(st, Options{}); d.Kind != KindNotInstalled {
		t.Fatalf("kind = %s, want not-installed", d.Kind)
	}
}

func TestElevationOnWindowsIsExplainedNotRun(t *testing.T) {
	routes := []provision.Method{{Tool: "gcc", Manager: "choco", Argv: []string{"choco", "install", "-y", "mingw"}, Elevated: true, Present: true}}
	st := Assemble(Inputs{Tool: "gcc", OS: "windows", Env: map[string]string{"PATH": `C:\x`}, Routes: routes})
	d := Diagnose(st, Options{})
	if d.Kind != KindNeedsAdmin {
		t.Fatalf("kind = %s, want needs-admin", d.Kind)
	}
	p := Repair(st, d, Options{})
	if p.CanRun || !p.Steps[0].Explain || p.Steps[0].Command != "choco install -y mingw" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestNoSudoBinaryAtAllSuggestsRoot(t *testing.T) {
	old := provision.SudoProbe
	provision.SudoProbe = func(map[string]string) bool { return false }
	defer func() { provision.SudoProbe = old }()
	env := map[string]string{"PATH": t.TempDir()} // empty directory: no sudo
	routes := []provision.Method{{Tool: "gcc", Manager: "apk", Argv: []string{"apk", "add", "gcc"}, Elevated: true, Present: true}}
	st := Assemble(Inputs{Tool: "gcc", OS: "linux", Env: env, Routes: routes})
	p := Repair(st, Diagnose(st, Options{}), Options{})
	if strings.HasPrefix(p.Steps[0].Command, "sudo ") {
		t.Fatalf("told the user to run sudo on a machine that has none: %q", p.Steps[0].Command)
	}
	if !strings.Contains(p.Steps[0].Note, "su -") {
		t.Fatalf("note should explain becoming root: %q", p.Steps[0].Note)
	}
}

// Review focus: a direct download lands in cache/downloads/<tool>, and the plan
// points at that exact path.
func TestDirectDownloadPlanNamesTheLandingPath(t *testing.T) {
	routes := []provision.Method{{Tool: "zig", Manager: "direct", URL: "https://example.test/zig.zip", Note: "unzip it and add it to PATH", Present: true}}
	st := Assemble(Inputs{Tool: "zig", OS: "linux", Env: map[string]string{"PATH": "/usr/bin"}, Routes: routes})
	cache := t.TempDir()
	p := Repair(st, Diagnose(st, Options{}), Options{CacheDir: cache})
	want := filepath.Join(cache, "downloads", "zig")
	if p.Lands != want || !strings.Contains(p.Steps[0].Note, want) || !strings.Contains(p.Steps[0].Note, "unzip it") {
		t.Fatalf("plan = %+v, want the landing path %s and the post-install note", p, want)
	}
}

// Review focus: shell persistence. A line appended to ~/.bashrc does nothing
// for zsh, so the renderer must name the right profile and the exact line.
func TestPathAppendNamesTheProfileForEachShell(t *testing.T) {
	cases := []struct {
		shell      Shell
		os         string
		file, line string
	}{
		{ShellBash, "linux", "~/.bashrc", `export PATH=/opt/llvm/bin:"$PATH"`},
		{ShellZsh, "linux", "~/.zshrc", `export PATH=/opt/llvm/bin:"$PATH"`},
		{ShellBash, "macos", "~/.bash_profile", `export PATH=/opt/llvm/bin:"$PATH"`},
		{ShellFish, "linux", "~/.config/fish/config.fish", "fish_add_path /opt/llvm/bin"},
	}
	for _, c := range cases {
		pl := PathAppend("/opt/llvm/bin", c.shell, c.os)
		if pl.File != c.file || pl.Line != c.line {
			t.Errorf("%s/%s: got file %q line %q, want %q %q", c.os, c.shell, pl.File, pl.Line, c.file, c.line)
		}
	}
	if PathAppend("/x", ShellBash, "linux").File == PathAppend("/x", ShellZsh, "linux").File {
		t.Error("bash and zsh share a profile file")
	}
}

func TestPathAppendQuotesHostileDirectories(t *testing.T) {
	if got := PathAppend("/opt/my llvm/bin", ShellBash, "linux").Line; got != `export PATH='/opt/my llvm/bin':"$PATH"` {
		t.Errorf("spaces not quoted: %s", got)
	}
	if got := PathAppend("/opt/it's/bin", ShellZsh, "linux").Line; got != `export PATH='/opt/it'\''s/bin':"$PATH"` {
		t.Errorf("single quote not escaped: %s", got)
	}
	if got := PathAppend("/opt/$HOME/bin", ShellBash, "linux").Line; !strings.Contains(got, `'/opt/$HOME/bin'`) {
		t.Errorf("a dollar sign would expand: %s", got)
	}
	ps := PathAppend(`C:\Program Files\it's\bin`, ShellPowerShell, "windows")
	if !strings.Contains(ps.Line, `'C:\Program Files\it''s\bin'`) {
		t.Errorf("powershell quote not doubled: %s", ps.Line)
	}
}

func TestCmdNeverRecommendsSetx(t *testing.T) {
	pl := PathAppend(`C:\tools\bin`, ShellCmd, "windows")
	if strings.Contains(pl.Line, "setx") {
		t.Fatalf("setx truncates PATH at 1024 chars: %s", pl.Line)
	}
	if !strings.Contains(pl.Note, "setx") {
		t.Fatal("the note should say why setx is avoided")
	}
}

func TestRepairForZshAppendsToZshrc(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin", "HOME": "/nonexistent-home"}
	st := Assemble(Inputs{Tool: "go", OS: "linux", Shell: ShellZsh, Env: env, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	p := Repair(st, Diagnose(st, Options{}), Options{})
	if p.Shape != ShapePath || p.Steps[0].File != "~/.zshrc" {
		t.Fatalf("plan = %+v, want a PATH line for ~/.zshrc", p)
	}
}

func TestUnknownShellOnWindowsShowsBothShells(t *testing.T) {
	env := map[string]string{"PATH": `C:\Windows`}
	st := Assemble(Inputs{Tool: "go", OS: "windows", Env: env, Installs: []detect.Install{inst(`C:\Go\bin\go.exe`, true)}})
	p := Repair(st, Diagnose(st, Options{}), Options{})
	seen := map[Shell]bool{}
	for _, s := range p.Steps {
		seen[s.Shell] = true
	}
	if !seen[ShellPowerShell] || !seen[ShellCmd] {
		t.Fatalf("an unknown Windows shell must render PowerShell and cmd blocks, got %v", seen)
	}
}

func TestDetectShell(t *testing.T) {
	if got := DetectShell(map[string]string{"SHELL": "/usr/bin/zsh"}, "linux"); got != ShellZsh {
		t.Errorf("zsh = %q", got)
	}
	if got := DetectShell(map[string]string{"SHELL": "/bin/bash", "ZEDRUN_SHELL": "fish"}, "linux"); got != ShellFish {
		t.Errorf("ZEDRUN_SHELL must win, got %q", got)
	}
	if got := DetectShell(map[string]string{"PSModulePath": `C:\x`}, "windows"); got != ShellUnknown {
		t.Errorf("PSModulePath is inherited by cmd and must not decide: %q", got)
	}
	if _, ok := ParseShell("tcsh"); ok {
		t.Error("an unsupported shell was accepted")
	}
}

func TestRenderPlanIsCopyPasteSafe(t *testing.T) {
	p := Plan{Tool: "go", Summary: "s", Kind: KindNotOnPath, Steps: []Step{{Title: "t", Command: `export PATH=/a:"$PATH"`}}, Verify: []string{"go version"}}
	var b strings.Builder
	RenderPlan(&b, p)
	out := b.String()
	if !strings.Contains(out, "```\nexport PATH=/a:\"$PATH\"\n```") {
		t.Fatalf("command is not alone in a fence:\n%s", out)
	}
	if strings.Contains(out, "$ export") {
		t.Fatalf("a prompt character would break pasting:\n%s", out)
	}
}

func TestVerifyOnlyPassesOnFreshEvidence(t *testing.T) {
	env := map[string]string{"PATH": "/opt/go/bin"}
	st := Assemble(Inputs{Tool: "go", OS: "linux", Env: env, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	if r := Verify(st, Diagnose(st, Options{})); !r.OK || r.Path != "/opt/go/bin/go" || r.Version == "" {
		t.Fatalf("verify = %+v", r)
	}
	bad := Assemble(Inputs{Tool: "go", OS: "linux", Env: map[string]string{"PATH": "/usr/bin"}})
	if r := Verify(bad, Diagnose(bad, Options{})); r.OK {
		t.Fatalf("verify passed with nothing installed: %+v", r)
	}
}

// writeFakeGo drops an executable named go that prints a Go version banner.
func writeFakeGo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho 'go version go1.99.1 linux/amd64'\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetectFindsARealExecutableOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake executable")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	writeFakeGo(t, bin)
	env := map[string]string{"PATH": bin, "HOME": home}
	cache := detect.NewProbeCache(filepath.Join(home, "probe.json"))
	st, err := Detect(context.Background(), config.Default(), cache, env, "linux", "go", false)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Present || st.Active == nil || st.Active.Path != filepath.Join(bin, "go") || !strings.Contains(st.Active.Version, "1.99.1") {
		t.Fatalf("status = %+v", st)
	}
	if d := Diagnose(st, Options{}); d.Kind != KindOK {
		t.Fatalf("kind = %s", d.Kind)
	}
}

func TestDetectReportsOffPathInstallFromExtraDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake executable")
	}
	home := t.TempDir()
	extra := filepath.Join(home, "sdk", "go", "bin")
	writeFakeGo(t, extra)
	cfg := config.Default()
	cfg.ExtraDirs = []string{extra}
	env := map[string]string{"PATH": t.TempDir(), "HOME": home}
	cache := detect.NewProbeCache(filepath.Join(home, "probe.json"))
	st, err := Detect(context.Background(), cfg, cache, env, "linux", "go", false)
	if err != nil {
		t.Fatal(err)
	}
	d := Diagnose(st, Options{ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }})
	if d.Kind != KindNotOnPath || d.Dir != extra {
		t.Fatalf("diagnosis = %+v, want not-on-path at %s", d, extra)
	}
}

func TestDetectRejectsUnknownTool(t *testing.T) {
	_, err := Detect(context.Background(), config.Default(), detect.NewProbeCache(""), map[string]string{}, "linux", "nope", false)
	if _, ok := err.(*UnknownToolError); !ok {
		t.Fatalf("err = %v, want *UnknownToolError", err)
	}
}

func TestDetectHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Detect(ctx, config.Default(), detect.NewProbeCache(""), map[string]string{}, "linux", "go", false); err == nil {
		t.Fatal("a cancelled context was ignored")
	}
}
