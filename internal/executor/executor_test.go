package executor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/planner"
)

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunsStepsAndReturnsExitCode(t *testing.T) {
	dir := t.TempDir()
	ok := script(t, dir, "ok", "exit 0")
	bad := script(t, dir, "bad", "exit 7")

	var buf bytes.Buffer
	if code := Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{ok}, Cwd: dir}}}, config.Default(), &buf, 5*time.Second, false, false); code != 0 {
		t.Fatalf("ok step returned %d", code)
	}
	buf.Reset()
	if code := Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{bad}, Cwd: dir}}}, config.Default(), &buf, 5*time.Second, false, false); code != 7 {
		t.Fatalf("failing step returned %d, want 7", code)
	}
}

func TestFailedBuildDoesNotRunStaleExe(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	stale := script(t, dir, "prog", "echo ran >> "+ran)
	compile := script(t, dir, "compile", "exit 2")

	plan := planner.Plan{Steps: []planner.Step{
		{Argv: []string{compile}, Cwd: dir, Label: "compile"},
		{Argv: []string{stale}, Cwd: dir, Label: "run"},
	}}
	var buf bytes.Buffer
	if code := Execute(plan, config.Default(), &buf, 5*time.Second, false, false); code != 2 {
		t.Fatalf("exit = %d, want the compile's 2", code)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("the stale binary ran after a failed build")
	}
}

func TestFooterPrinted(t *testing.T) {
	dir := t.TempDir()
	ok := script(t, dir, "ok", "exit 0")
	var buf bytes.Buffer
	Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{ok}, Cwd: dir}}}, config.Default(), &buf, 5*time.Second, false, false)
	if !strings.Contains(buf.String(), "[Done] exit code 0") {
		t.Fatalf("footer missing: %q", buf.String())
	}

	cfg := config.Default()
	cfg.ShowFooter = false
	buf.Reset()
	Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{ok}, Cwd: dir}}}, cfg, &buf, 5*time.Second, false, false)
	if strings.Contains(buf.String(), "[Done]") {
		t.Fatalf("footer printed despite ShowFooter=false: %q", buf.String())
	}
}

func TestTimeoutKillsChild(t *testing.T) {
	dir := t.TempDir()
	slow := script(t, dir, "slow", "sleep 5")
	var buf bytes.Buffer
	start := time.Now()
	code := Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{slow}, Cwd: dir}}}, config.Default(), &buf, 300*time.Millisecond, false, false)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Execute waited %v, want it to give up near the timeout", elapsed)
	}
	if code == 0 {
		t.Fatal("a timed-out step returned 0")
	}
}

func TestPathPrefixApplied(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "toolbin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatal(err)
	}
	show := script(t, dir, "showpath", `echo "$PATH"`)

	var buf bytes.Buffer
	Execute(planner.Plan{
		Steps:      []planner.Step{{Argv: []string{show}, Cwd: dir}},
		PathPrefix: []string{prefix},
	}, config.Default(), &buf, 5*time.Second, false, false)

	line := strings.SplitN(buf.String(), "\n", 2)[0]
	first := strings.Split(line, string(os.PathListSeparator))[0]
	if first != prefix {
		t.Fatalf("PATH starts with %q, want %q (full: %q)", first, prefix, line)
	}
}

func TestEnvOverrideApplied(t *testing.T) {
	dir := t.TempDir()
	show := script(t, dir, "showenv", `echo "$ZEDRUN_TEST_VAR"`)
	plan := planner.Plan{
		Steps: []planner.Step{{Argv: []string{show}, Cwd: dir}},
		Env:   map[string]string{"ZEDRUN_TEST_VAR": "hello"},
	}
	var buf bytes.Buffer
	Execute(plan, config.Default(), &buf, 5*time.Second, false, false)
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("env override not applied: %q", buf.String())
	}
}

func TestScriptsWrittenBeforeRun(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "wrapper.sh")
	use := script(t, dir, "use", "cat "+bat)

	plan := planner.Plan{
		Steps:   []planner.Step{{Argv: []string{use}, Cwd: dir}},
		Scripts: map[string]string{bat: "HELLO-FROM-SCRIPT\n"},
	}
	var buf bytes.Buffer
	if code := Execute(plan, config.Default(), &buf, 5*time.Second, false, false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(buf.String(), "HELLO-FROM-SCRIPT") {
		t.Fatalf("script not written before the run: %q", buf.String())
	}
}

func TestEchoCommands(t *testing.T) {
	dir := t.TempDir()
	ok := script(t, dir, "ok", "exit 0")
	var buf bytes.Buffer
	Execute(planner.Plan{Steps: []planner.Step{{Argv: []string{ok}, Cwd: dir}}}, config.Default(), &buf, 5*time.Second, true, false)
	if !strings.Contains(buf.String(), "> ") {
		t.Fatalf("echo did not print the command: %q", buf.String())
	}
}

func TestDescribeExit(t *testing.T) {
	if DescribeExit(0, "linux") != "" {
		t.Errorf("DescribeExit(0) = %q, want empty", DescribeExit(0, "linux"))
	}
	if got := DescribeExit(-1073741515, "windows"); !strings.Contains(got, "0xC0000135") {
		t.Errorf("DescribeExit(0xC0000135) = %q, want it to name the NTSTATUS", got)
	}
	if got := DescribeExit(-1073741515, "linux"); strings.Contains(got, "0xC0000135") {
		t.Errorf("DescribeExit treated a POSIX signal as an NTSTATUS: %q", got)
	}
	if got := DescribeExit(-11, "linux"); !strings.Contains(got, "signal 11") {
		t.Errorf("DescribeExit(-11) = %q, want signal 11", got)
	}
	if got := DescribeExit(124, "linux"); got == "" {
		t.Error("DescribeExit(124) is empty, want a timeout note")
	}
}
