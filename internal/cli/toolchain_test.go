package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/provision"
	"github.com/zedrun/zedrun-go/internal/toolchain"
)

const fakeGoBody = "#!/bin/sh\necho 'go version go1.99.1 linux/amd64'\n"

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("posix fake executables")
	}
}

// notRoot makes the process look unprivileged for the test, so the
// elevation rules are exercised even when the suite runs as root.
func notRoot(t *testing.T) {
	t.Helper()
	old := isRoot
	isRoot = func() bool { return false }
	t.Cleanup(func() { isRoot = old })
}

// offPathGo installs a working fake go in a directory that is not on PATH and
// registers it through extraDirs, the supported way to point zedrun-go at one.
func offPathGo(t *testing.T, opts Options) string {
	t.Helper()
	dir := filepath.Join(opts.Env["HOME"], "sdk", "go", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(fakeGoBody), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(opts.Env["HOME"], ".zedrun.json"), `{"extraDirs": ["`+dir+`"]}`)
	return dir
}

func TestToolchainHelp(t *testing.T) {
	opts := getOpts(t)
	code, out, _ := run(t, opts, "toolchain", "--help")
	if code != 0 || !strings.Contains(out, "set-path") || !strings.Contains(out, "resume") {
		t.Fatalf("help: code=%d\n%s", code, out)
	}
	if code, _, errOut := run(t, opts, "toolchain"); code != 2 || !strings.Contains(errOut, "usage: zedrun-go toolchain") {
		t.Fatalf("bare toolchain: code=%d\n%s", code, errOut)
	}
	code, _, errOut := run(t, opts, "toolchain", "diagnos", "go")
	if code != 2 || !strings.Contains(errOut, `did you mean "diagnose"`) {
		t.Fatalf("typo: code=%d\n%s", code, errOut)
	}
	if !strings.Contains(usage, "toolchain") {
		t.Fatal("the top-level usage does not list toolchain")
	}
}

func TestToolchainArgumentParsing(t *testing.T) {
	opts := getOpts(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"toolchain", "diagnose"}, "needs a tool"},
		{[]string{"toolchain", "diagnose", "nosuch"}, `unknown tool "nosuch"`},
		{[]string{"toolchain", "status", "go", "gcc"}, "takes one argument"},
		{[]string{"toolchain", "how-to", "go", "--shell", "tcsh"}, `unknown shell "tcsh"`},
		{[]string{"toolchain", "how-to", "go", "--shell"}, "needs a value"},
		{[]string{"toolchain", "repair", "go", "--bogus"}, "unknown flag --bogus"},
		{[]string{"toolchain", "repair", "go", "--timeout", "never"}, `bad --timeout "never"`},
	}
	for _, c := range cases {
		code, _, errOut := run(t, opts, c.args...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: code=%d stderr=%q, want exit 2 containing %q", c.args, code, errOut, c.want)
		}
	}
}

func TestToolchainStatusJSONForAToolOnPath(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	put(t, opts, "go", fakeGoBody)
	code, out, errOut := run(t, opts, "toolchain", "status", "go", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var rep struct {
		Status    struct{ Present bool }
		Diagnosis struct{ Kind string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if !rep.Status.Present || rep.Diagnosis.Kind != "ok" {
		t.Fatalf("report = %+v\n%s", rep, out)
	}
}

func TestToolchainDiagnoseExitCodeTracksHealth(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	put(t, opts, "go", fakeGoBody)
	if code, _, errOut := run(t, opts, "toolchain", "diagnose", "go", "--no-state"); code != 0 {
		t.Fatalf("healthy tool exit %d: %s", code, errOut)
	}

	bad := getOpts(t)
	dir := offPathGo(t, bad)
	code, out, _ := run(t, bad, "toolchain", "diagnose", "go", "--no-state", "--shell", "zsh")
	if code != 1 {
		t.Fatalf("a tool off PATH must exit 1, got %d\n%s", code, out)
	}
	for _, want := range []string{"not-on-path", dir, "~/.zshrc", `export PATH=` + dir + `:"$PATH"`, "NEW terminal"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnose output is missing %q:\n%s", want, out)
		}
	}
}

func TestToolchainHowToNeverRunsAnything(t *testing.T) {
	skipWindows(t)
	notRoot(t)
	opts := getOpts(t)
	marker := filepath.Join(opts.Env["HOME"], "ran")
	// apt-get is also probed with --version; only an install may leave the marker.
	put(t, opts, "apt-get", "#!/bin/sh\ncase \"$*\" in *install*) touch "+marker+";; esac\n")
	putSudo(t, opts)
	code, out, _ := run(t, opts, "toolchain", "how-to", "ruby")
	if code != 0 || !strings.Contains(out, "toolchain repair ruby --run --yes") {
		t.Fatalf("how-to: code=%d\n%s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("how-to executed the package manager")
	}
}

// A fake apt-get that installs a working gcc into the PATH directory.
func fakeAptInstallingRuby(t *testing.T, opts Options) (marker string) {
	t.Helper()
	bin := filepath.Join(opts.Env["HOME"], "bin")
	marker = filepath.Join(opts.Env["HOME"], "apt-ran")
	script := "#!/bin/sh\ncase \"$*\" in *install*) ;; *) exit 0 ;; esac\necho \"$@\" > " + marker + "\n" +
		"printf '#!/bin/sh\\necho \"ruby 3.3.0\"\\n' > " + bin + "/ruby\n/bin/chmod +x " + bin + "/ruby\n"
	put(t, opts, "apt-get", script)
	return marker
}

func TestToolchainRepairPreviewThenRunAndVerify(t *testing.T) {
	skipWindows(t)
	notRoot(t)
	opts := getOpts(t)
	putSudo(t, opts)
	marker := fakeAptInstallingRuby(t, opts)

	// Without --run: a preview, nothing executes.
	code, out, _ := run(t, opts, "toolchain", "repair", "ruby", "--no-state")
	if code != 0 || !strings.Contains(out, "--run --yes") {
		t.Fatalf("preview: code=%d\n%s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repair without --run executed the installer")
	}
	// --run without --yes: asks, still nothing executes.
	code, out, _ = run(t, opts, "toolchain", "repair", "ruby", "--run", "--no-state")
	if code != 0 || !strings.Contains(out, "re-run with --yes") {
		t.Fatalf("--run without --yes: code=%d\n%s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("--run without --yes executed the installer")
	}
	// --run --yes: installs, then proves it with a fresh probe.
	code, out, errOut := run(t, opts, "toolchain", "repair", "ruby", "--run", "--yes", "--no-state")
	if code != 0 {
		t.Fatalf("repair: code=%d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "verified: ruby 3.3.0") {
		t.Fatalf("success was claimed without fresh evidence:\n%s", out)
	}
	got, _ := os.ReadFile(marker)
	if !strings.Contains(string(got), "install -y ruby-full") {
		t.Fatalf("apt-get ran with %q", got)
	}
}

// Review focus: with `sudo -n` failing the tool must not run the install and
// must return the exact command for the user to run.
func TestToolchainRepairWithoutPasswordlessSudoExplainsTheExactCommand(t *testing.T) {
	skipWindows(t)
	notRoot(t)
	opts := getOpts(t)
	marker := fakeAptInstallingRuby(t, opts)
	// sudo exists but wants a password: `sudo -n` fails.
	put(t, opts, "sudo", "#!/bin/sh\nexit 1\n")

	code, out, _ := run(t, opts, "toolchain", "repair", "ruby", "--run", "--yes", "--no-state")
	if code != 1 {
		t.Fatalf("an unrunnable elevated install must exit 1, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "sudo apt-get install -y ruby-full") {
		t.Fatalf("the exact user command is missing:\n%s", out)
	}
	if !strings.Contains(out, "will not ask for or accept a password") {
		t.Fatalf("the stop condition is missing:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the installer ran without elevation")
	}
}

func TestToolchainRepairReportsAFinishedInstallThatIsStillNotUsable(t *testing.T) {
	skipWindows(t)
	notRoot(t)
	opts := getOpts(t)
	putSudo(t, opts)
	// An installer that exits 0 but puts nothing anywhere we can find.
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	code, out, _ := run(t, opts, "toolchain", "repair", "ruby", "--run", "--yes", "--no-state")
	if code != 1 || strings.Contains(out, "verified:") {
		t.Fatalf("claimed success on exit code alone: code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "still not usable") {
		t.Fatalf("the failure was not reported:\n%s", out)
	}
}

func TestToolchainVerifyExitCodes(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	put(t, opts, "go", fakeGoBody)
	code, out, _ := run(t, opts, "toolchain", "verify", "go", "--no-state")
	if code != 0 || !strings.Contains(out, "ok: go go version go1.99.1") && !strings.Contains(out, "ok: go") {
		t.Fatalf("verify healthy: code=%d\n%s", code, out)
	}
	code, out, _ = run(t, getOpts(t), "toolchain", "verify", "gcc", "--no-state")
	if code != 1 || !strings.Contains(out, "not verified") {
		t.Fatalf("verify missing: code=%d\n%s", code, out)
	}
	code, out, _ = run(t, getOpts(t), "toolchain", "verify", "gcc", "--no-state", "--json")
	var r struct{ OK bool }
	if code != 1 || json.Unmarshal([]byte(out), &r) != nil || r.OK {
		t.Fatalf("verify --json: code=%d\n%s", code, out)
	}
}

func TestToolchainStatusListsUnhealthyToolchains(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	offPathGo(t, opts)
	code, out, _ := run(t, opts, "toolchain", "status")
	// The host may have its own toolchains in well-known dirs, so look for
	// go's row rather than counting rows.
	if code != 0 || !strings.Contains(out, "unhealthy") || !regexp.MustCompile(`(?m)^\s+go\s+not-on-path`).MatchString(out) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	healthy := getOpts(t)
	put(t, healthy, "go", fakeGoBody)
	code, out, _ = run(t, healthy, "toolchain", "status")
	if code != 0 || regexp.MustCompile(`(?m)^\s+go\s`).MatchString(out) {
		t.Fatalf("a go on PATH was reported unhealthy: code=%d\n%s", code, out)
	}
}

func TestToolchainSetPath(t *testing.T) {
	opts := getOpts(t)
	code, out, _ := run(t, opts, "toolchain", "set-path", "/opt/llvm/bin", "--shell", "bash")
	if code != 0 || !strings.Contains(out, "append to ~/.bashrc") || !strings.Contains(out, `export PATH=/opt/llvm/bin:"$PATH"`) {
		t.Fatalf("bash: code=%d\n%s", code, out)
	}
	_, out, _ = run(t, opts, "toolchain", "set-path", "/opt/llvm/bin", "--shell", "zsh")
	if !strings.Contains(out, "append to ~/.zshrc") || strings.Contains(out, "append to ~/.bashrc") {
		t.Fatalf("zsh output must name ~/.zshrc and never ~/.bashrc:\n%s", out)
	}
	if code, _, errOut := run(t, opts, "toolchain", "set-path", "tools/bin"); code != 2 || !strings.Contains(errOut, "not an absolute path") {
		t.Fatalf("relative path: code=%d %s", code, errOut)
	}
	if code, _, _ := run(t, opts, "toolchain", "set-path"); code != 2 {
		t.Fatalf("missing dir: code=%d", code)
	}
}

func TestToolchainSetPathOnWindowsShowsBothShells(t *testing.T) {
	opts := getOpts(t)
	opts.OS = "windows"
	opts.Env["PATH"] = `C:\Windows`
	code, out, _ := run(t, opts, "toolchain", "set-path", `C:\Program Files\LLVM\bin`)
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, want := range []string{"powershell", "cmd", `'C:\Program Files\LLVM\bin'`, "SetEnvironmentVariable"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "```\nsetx") {
		t.Errorf("setx PATH truncates and must not be offered:\n%s", out)
	}
}

func TestToolchainResumeRoundTrip(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	offPathGo(t, opts)

	if code, out, _ := run(t, opts, "toolchain", "resume"); code != 1 || !strings.Contains(out, "no resume state") {
		t.Fatalf("empty resume: code=%d\n%s", code, out)
	}
	// diagnose records the problem...
	run(t, opts, "toolchain", "diagnose", "go", "--shell", "bash")
	code, out, _ := run(t, opts, "toolchain", "resume")
	if code != 0 || !strings.Contains(out, "[pending] go") || !strings.Contains(out, "re-verify subject #1") {
		t.Fatalf("resume after diagnose: code=%d\n%s", code, out)
	}
	// ...and a later verify, after the fix, closes it.
	put(t, opts, "go", fakeGoBody)
	run(t, opts, "toolchain", "verify", "go")
	code, out, _ = run(t, opts, "toolchain", "resume")
	if code != 0 || !strings.Contains(out, "[done] go") || !strings.Contains(out, "nothing pending") {
		t.Fatalf("resume after verify: code=%d\n%s", code, out)
	}
	code, out, _ = run(t, opts, "toolchain", "resume", "--json")
	var js struct {
		State struct{ State string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &js) != nil || js.State.State != "done" {
		t.Fatalf("resume --json: code=%d\n%s", code, out)
	}
}

func TestToolchainNoStateWritesNothing(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	offPathGo(t, opts)
	run(t, opts, "toolchain", "diagnose", "go", "--no-state")
	if p := toolchain.StatePath(opts.Env, "linux", filepath.Base(opts.ProjectStart)); fileExists(p) {
		t.Fatalf("--no-state still wrote %s", p)
	}
}

// Review focus: a corrupt resume file is ignored with a warning and the
// command still does its job.
func TestToolchainCorruptStateIsIgnoredNotFatal(t *testing.T) {
	skipWindows(t)
	opts := getOpts(t)
	offPathGo(t, opts)
	path := toolchain.StatePath(opts.Env, "linux", filepath.Base(opts.ProjectStart))
	writeFile(t, path, `{"schema":1,"subjects":[{"id":1,"sym`)
	code, out, errOut := run(t, opts, "toolchain", "diagnose", "go", "--shell", "bash")
	if code != 1 || !strings.Contains(out, "not-on-path") {
		t.Fatalf("diagnose must still work: code=%d\n%s", code, out)
	}
	if !strings.Contains(errOut, "ignoring unreadable resume state") {
		t.Fatalf("the ignored file was not reported:\n%s", errOut)
	}
	if _, ok, err := toolchain.Load(path); !ok || err != nil {
		t.Fatalf("the corrupt file was not replaced by a good one: ok=%v err=%v", ok, err)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// Review focus: a direct download streams to cache/downloads/<tool> and the
// closing message carries the method's Note, which is the command to run.
func TestExecDownloadWritesTheFileAndPrintsTheNote(t *testing.T) {
	body := bytes.Repeat([]byte("zedrun"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12288")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	opts := getOpts(t)
	m := provision.Method{Tool: "zig", Manager: "direct", URL: srv.URL + "/zig.zip", Note: "unzip it and add the folder to PATH", Present: true}
	var out, errOut bytes.Buffer
	if rc := execDownload(m, &out, &errOut, opts, opts.Env, "linux", 0); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, errOut.String())
	}
	dest := filepath.Join(platform.CacheDir(opts.Env, "linux"), "downloads", "zig")
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the file did not land at %s intact: err=%v len=%d", dest, err, len(got))
	}
	for _, want := range []string{"downloaded zig to " + dest, "run it with: unzip it and add the folder to PATH", "100%"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out.String())
		}
	}
}

func TestExecDownloadFailureLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	opts := getOpts(t)
	m := provision.Method{Tool: "zig", Manager: "direct", URL: srv.URL + "/missing", Note: "n"}
	var out, errOut bytes.Buffer
	if rc := execDownload(m, &out, &errOut, opts, opts.Env, "linux", 0); rc != 1 {
		t.Fatalf("rc=%d, want 1", rc)
	}
	dest := filepath.Join(platform.CacheDir(opts.Env, "linux"), "downloads", "zig")
	if fileExists(dest) {
		t.Fatal("a failed download left a file the plan would point at")
	}
	if !strings.Contains(errOut.String(), "download failed") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}
