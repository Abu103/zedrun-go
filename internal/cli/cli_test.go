package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// testOpts isolates config/cache in a temp dir but keeps the real PATH so real
// toolchains (python3, gcc) are found.
func testOpts(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	e := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			e[kv[:i]] = kv[i+1:]
		}
	}
	e["HOME"] = home
	e["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
	e["XDG_CACHE_HOME"] = filepath.Join(home, ".cache")
	e["XDG_DATA_HOME"] = filepath.Join(home, ".local", "share")
	return Options{Env: e, OS: "linux", Cache: detect.NewProbeCache(""), ProjectStart: home}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, opts Options, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := MainWith(args, &out, &errOut, opts)
	return code, out.String(), errOut.String()
}

func TestRunPythonFileExitCode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "prog.py")
	writeFile(t, f, "import sys\nsys.exit(3)\n")

	code, _, errOut := run(t, testOpts(t), "run", f)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stderr=%s)", code, errOut)
	}
}

func TestRunPathWithSpacesAndUnicode(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "my dir", "ünïcode")
	f := filepath.Join(sub, "prógram with spaces.py")
	writeFile(t, f, "print('ok-from-unicode')\n")

	code, out, errOut := run(t, testOpts(t), "run", f)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	if !strings.Contains(out, "ok-from-unicode") {
		t.Fatalf("program output missing:\n%s", out)
	}
}

func TestRunMissingFileExit1(t *testing.T) {
	code, _, errOut := run(t, testOpts(t), "run", "/nonexistent/thing.py")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "not found") {
		t.Fatalf("stderr = %q, want a not-found message", errOut)
	}
}

func TestRunCWithGcc(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("no gcc on this machine")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "hello.c")
	writeFile(t, f, "#include <stdio.h>\nint main(void){printf(\"hi-from-c\\n\");return 0;}\n")

	code, out, errOut := run(t, testOpts(t), "run", f)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "hi-from-c") {
		t.Fatalf("no program output:\n%s", out)
	}
}

func TestRunNoToolchainExit127(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".zedrun.json"),
		`{"languages": {"weird": {"exts": [".weird"], "run": ["nosuchtool-xyz {file}"]}}}`)
	f := filepath.Join(dir, "a.weird")
	writeFile(t, f, "x\n")

	opts := testOpts(t)
	opts.ProjectStart = dir
	code, _, errOut := run(t, opts, "run", f)
	if code != 127 {
		t.Fatalf("exit = %d, want 127 (stderr=%s)", code, errOut)
	}
	if !strings.Contains(errOut, "nosuchtool-xyz") {
		t.Fatalf("stderr = %q, want it to name the toolchain it tried", errOut)
	}
}

func TestArgsAfterDoubleDash(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "args.py")
	writeFile(t, f, "import sys\nprint('ARGS:' + ' '.join(sys.argv[1:]))\n")

	code, out, errOut := run(t, testOpts(t), "run", f, "--", "--release", "x y")
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	if !strings.Contains(out, "ARGS:--release x y") {
		t.Fatalf("program args not forwarded:\n%s", out)
	}
}

func TestWhichShowsChoice(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "prog.py")
	writeFile(t, f, "print(1)\n")

	code, out, errOut := run(t, testOpts(t), "which", f)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	if !strings.Contains(out, "python") {
		t.Fatalf("which output does not name the toolchain:\n%s", out)
	}
	if !strings.Contains(out, "python") || !strings.Contains(out, f) && !strings.Contains(out, "prog.py") {
		t.Fatalf("which output is missing detail:\n%s", out)
	}
}

func TestLanguagesLists(t *testing.T) {
	code, out, _ := run(t, testOpts(t), "languages")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"python", ".py", "c", ".c", "typescript", ".ts"} {
		if !strings.Contains(out, want) {
			t.Errorf("languages output missing %q:\n%s", want, out)
		}
	}
}

func TestUseWritesPreference(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")

	code, _, errOut := run(t, testOpts(t), "use", "c", "clang", "--config", cfgPath, "--path", "/usr/bin/clang")
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, data)
	}
	prefer, _ := m["prefer"].(map[string]any)
	if prefer["c"] != "clang" {
		t.Fatalf("prefer.c = %v, want clang\n%s", prefer["c"], data)
	}
}

func TestPickListsInstalls(t *testing.T) {
	code, out, _ := run(t, testOpts(t), "pick", "python")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "python") {
		t.Fatalf("pick output:\n%s", out)
	}
}

func TestOutdatedJSONWithFakeFetcher(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	writeFile(t, node, "#!/bin/sh\necho 'v20.0.0'\n")
	if err := os.Chmod(node, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".zedrun.json"), `{"paths": {"node": "`+node+`"}}`)

	opts := testOpts(t)
	opts.ProjectStart = dir
	opts.Fetcher = sources.NewFetcher("", time.Hour, func(url string) ([]byte, error) {
		switch url {
		case "https://nodejs.org/dist/index.json":
			return []byte(`[{"version":"v22.10.0","lts":"Jod"},{"version":"v23.1.0","lts":false}]`), nil
		case "https://nodejs.org/download/rc/index.json":
			return []byte(`[]`), nil
		}
		return nil, os.ErrNotExist
	})

	code, out, errOut := run(t, opts, "outdated", "--json")
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	var found bool
	for _, r := range rows {
		if r["tool"] == "node" {
			found = true
			if r["status"] != "outdated" {
				t.Errorf("node status = %v, want outdated (installed v20, latest v22.10.0)", r["status"])
			}
			if r["bump"] != "major" {
				t.Errorf("node bump = %v, want major", r["bump"])
			}
		}
	}
	if !found {
		t.Fatalf("no node row in the report:\n%s", out)
	}
}

func TestOutdatedPreFlag(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	writeFile(t, node, "#!/bin/sh\necho 'v22.10.0'\n")
	if err := os.Chmod(node, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".zedrun.json"), `{"paths": {"node": "`+node+`"}}`)

	opts := testOpts(t)
	opts.ProjectStart = dir
	opts.Fetcher = sources.NewFetcher("", time.Hour, func(url string) ([]byte, error) {
		switch url {
		case "https://nodejs.org/dist/index.json":
			return []byte(`[{"version":"v22.10.0","lts":"Jod"}]`), nil
		case "https://nodejs.org/download/rc/index.json":
			return []byte(`[{"version":"v24.0.0-rc.1","lts":false}]`), nil
		}
		return nil, os.ErrNotExist
	})

	// Targets are reported as bare versions now, so both halves have to use
	// that form — matching on the old "v" prefix would make the negative check
	// below pass no matter what the stable channel reported.
	_, stableOut, _ := run(t, opts, "outdated", "--json")
	if strings.Contains(stableOut, "24.0.0-rc.1") {
		t.Errorf("stable channel reported a pre-release target:\n%s", stableOut)
	}
	_, preOut, _ := run(t, opts, "outdated", "--json", "--pre")
	if !strings.Contains(preOut, "24.0.0-rc.1") {
		t.Errorf("--pre did not surface the pre-release target:\n%s", preOut)
	}
}

func TestOutdatedFailOnOutdatedExit1(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	writeFile(t, node, "#!/bin/sh\necho 'v20.0.0'\n")
	if err := os.Chmod(node, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".zedrun.json"), `{"paths": {"node": "`+node+`"}}`)

	opts := testOpts(t)
	opts.ProjectStart = dir
	opts.Fetcher = sources.NewFetcher("", time.Hour, func(url string) ([]byte, error) {
		switch url {
		case "https://nodejs.org/dist/index.json":
			return []byte(`[{"version":"v22.10.0","lts":"Jod"}]`), nil
		case "https://nodejs.org/download/rc/index.json":
			return []byte(`[]`), nil
		}
		return nil, os.ErrNotExist
	})

	if code, _, _ := run(t, opts, "outdated", "--fail-on-outdated"); code != 1 {
		t.Fatalf("exit = %d, want 1 when something is outdated", code)
	}
}

func TestDoctorRuns(t *testing.T) {
	code, out, errOut := run(t, testOpts(t), "doctor")
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	for _, want := range []string{"System", "Tools", "Languages"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestCleanRuns(t *testing.T) {
	code, _, errOut := run(t, testOpts(t), "clean")
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
}

func TestTypoCommandSuggests(t *testing.T) {
	code, _, errOut := run(t, testOpts(t), "isntall")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown command", code)
	}
	if !strings.Contains(errOut, "install") {
		t.Fatalf("stderr = %q, want a suggestion naming install", errOut)
	}
}

func TestInstallDryRunIntoZedDir(t *testing.T) {
	zed := t.TempDir()
	opts := testOpts(t)
	code, out, errOut := run(t, opts, "install", "--dry-run", "--zed-dir", zed)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%s)", code, errOut)
	}
	if !strings.Contains(out, "tasks.json") {
		t.Fatalf("install output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(zed, "tasks.json")); !os.IsNotExist(err) {
		t.Fatal("--dry-run wrote a file")
	}
}

func TestParseRunArgsFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFile string
		wantProg []string
		check    func(t *testing.T, f runFlags)
	}{
		{
			name:     "release",
			args:     []string{"main.go", "--release"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if !f.release {
					t.Fatal("--release not set")
				}
			},
		},
		{
			name:     "dry-run",
			args:     []string{"main.go", "--dry-run"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if !f.dryRun {
					t.Fatal("--dry-run not set")
				}
			},
		},
		{
			name:     "verbose",
			args:     []string{"main.go", "-v"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if !f.verbose {
					t.Fatal("-v not set")
				}
			},
		},
		{
			name:     "tool",
			args:     []string{"main.go", "--tool", "go1.22"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if f.tool != "go1.22" {
					t.Fatalf("--tool = %q, want go1.22", f.tool)
				}
			},
		},
		{
			name:     "build-dir",
			args:     []string{"main.go", "--build-dir", "/tmp/build"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if f.buildDir != "/tmp/build" {
					t.Fatalf("--build-dir = %q, want /tmp/build", f.buildDir)
				}
			},
		},
		{
			name:     "timeout duration",
			args:     []string{"main.go", "--timeout", "500ms"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if f.timeout != 500*time.Millisecond {
					t.Fatalf("--timeout = %v, want 500ms", f.timeout)
				}
			},
		},
		{
			name:     "timeout seconds",
			args:     []string{"main.go", "--timeout", "30"},
			wantFile: "main.go",
			check: func(t *testing.T, f runFlags) {
				if f.timeout != 30*time.Second {
					t.Fatalf("--timeout = %v, want 30s", f.timeout)
				}
			},
		},
		{
			name:     "double-dash passthrough",
			args:     []string{"main.go", "--", "--flag", "value"},
			wantFile: "main.go",
			wantProg: []string{"--flag", "value"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, prog, flags, err := parseRunArgs(tt.args)
			if err != nil {
				t.Fatalf("parseRunArgs error: %v", err)
			}
			if file != tt.wantFile {
				t.Fatalf("file = %q, want %q", file, tt.wantFile)
			}
			if strings.Join(prog, "|") != strings.Join(tt.wantProg, "|") {
				t.Fatalf("progArgs = %v, want %v", prog, tt.wantProg)
			}
			if tt.check != nil {
				tt.check(t, flags)
			}
		})
	}
}

func TestParseRunArgsFlagErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"main.go", "--nope"}, want: "unknown flag"},
		{name: "tool missing value", args: []string{"main.go", "--tool"}, want: "needs a value"},
		{name: "timeout missing value", args: []string{"main.go", "--timeout"}, want: "needs a value"},
		{name: "build-dir missing value", args: []string{"main.go", "--build-dir"}, want: "needs a value"},
		{name: "bad timeout", args: []string{"main.go", "--timeout", "abc"}, want: "bad --timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseRunArgs(tt.args)
			if err == nil {
				t.Fatalf("expected error for %v", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Every command must answer --help. `zedrun-go run --help` used to fail with
// "unknown flag --help" and exit 2.
func TestEveryCommandAnswersHelp(t *testing.T) {
	for _, cmd := range commandNames {
		code, out, errOut := run(t, testOpts(t), cmd, "--help")
		if code != 0 {
			t.Errorf("%s --help exit = %d, want 0 (stderr: %s)", cmd, code, errOut)
		}
		if !strings.Contains(out, "usage: zedrun-go "+cmd) {
			t.Errorf("%s --help did not print its own usage:\n%s", cmd, out)
		}
		if strings.Contains(errOut, "unknown flag") {
			t.Errorf("%s --help was still rejected as an unknown flag", cmd)
		}
	}
}

func TestWantsHelpStopsAtDoubleDash(t *testing.T) {
	if !wantsHelp([]string{"-h"}) || !wantsHelp([]string{"--help"}) {
		t.Fatal("wantsHelp should accept -h and --help")
	}
	if wantsHelp([]string{"--", "--help"}) {
		t.Fatal("wantsHelp must stop at --, so the program still gets --help")
	}
	if wantsHelp([]string{"main.py", "input.txt"}) {
		t.Fatal("wantsHelp should not fire on plain arguments")
	}
}

// Two installs of one tool share a tool id, so `use <lang> <tool>` cannot tell
// them apart — the hint has to offer --path instead.
func TestPickHintMentionsPathWhenInstallsCollide(t *testing.T) {
	opts := collidingPythonOpts(t)
	code, out, errOut := run(t, opts, "pick", "python")
	if code != 0 {
		t.Fatalf("pick exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "--path") {
		t.Fatalf("two installs share a tool id, so the hint must mention --path:\n%s", out)
	}
}

func TestDoctorSaysHowToPinBetweenCollidingInstalls(t *testing.T) {
	opts := collidingPythonOpts(t)
	code, out, errOut := run(t, opts, "doctor", "--all")
	if code != 0 {
		t.Fatalf("doctor exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "usable installs") {
		t.Skipf("no colliding installs were detected in this environment:\n%s", out)
	}
	if !strings.Contains(out, "--path") {
		t.Fatalf("doctor must say how to choose between colliding installs:\n%s", out)
	}
}

// collidingPythonOpts puts two executables named python3 on PATH, so the python
// tool id has two usable installs.
func collidingPythonOpts(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	var dirs []string
	for _, n := range []string{"a", "b"} {
		d := filepath.Join(home, n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, "python3")
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'Python 3.12.0'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, d)
	}
	opts := testOpts(t)
	opts.Env["PATH"] = strings.Join(dirs, string(os.PathListSeparator))
	return opts
}

func TestVersionFlag(t *testing.T) {
	code, out, _ := run(t, testOpts(t), "--version")
	if code != 0 || !strings.Contains(out, "zedrun-go") {
		t.Fatalf("--version → (%d, %q)", code, out)
	}
}
