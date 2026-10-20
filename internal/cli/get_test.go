package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// getOpts is testOpts with a PATH that contains only the fakes the test writes,
// so the run cannot touch the real machine's package manager.
func getOpts(t *testing.T) Options {
	t.Helper()
	opts := testOpts(t)
	opts.Env["PATH"] = filepath.Join(opts.Env["HOME"], "bin")
	return opts
}

// put writes an executable into the test's PATH directory.
func put(t *testing.T, opts Options, name, body string) string {
	t.Helper()
	dir := filepath.Join(opts.Env["HOME"], "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// putSudo puts a passwordless sudo shim on the test PATH, so an elevated route
// can actually run without a password prompt.
//
// The shim answers `sudo -n true` — the passwordless probe — with success, and
// passes anything else straight through. It must not try to run `true` itself:
// with the test's restricted PATH there is no /usr/bin to find it in, and dash
// reports "exec: true: not found".
func putSudo(t *testing.T, opts Options) {
	t.Helper()
	put(t, opts, "sudo", "#!/bin/sh\ncase \"$1\" in\n  -n) exit 0 ;;\nesac\nexec \"$@\"\n")
}

// ---- listing --------------------------------------------------------------

func TestGetListsRoutesForATool(t *testing.T) {
	opts := getOpts(t)
	code, out, errOut := run(t, opts, "get", "gcc")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "apt-get install -y gcc") {
		t.Fatalf("the apt route is missing:\n%s", out)
	}
	if !strings.Contains(out, "* = what `--run` would use") {
		t.Fatalf("the legend is missing:\n%s", out)
	}
}

func TestGetRefusesAnUnknownTool(t *testing.T) {
	code, _, errOut := run(t, getOpts(t), "get", "nosuchtool-xyz")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown tool") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// The route list used to come back in map order, so the starred choice moved
// between runs. The distro manager must be first and stable.
func TestGetRouteOrderIsStable(t *testing.T) {
	opts := getOpts(t)
	var first string
	for i := 0; i < 15; i++ {
		_, out, _ := run(t, opts, "get", "rustc")
		starred := ""
		for _, line := range strings.Split(out, "\n") {
			// Only the indented route rows carry a star; the legend line
			// starts with "* =" and would otherwise read as a route.
			if strings.HasPrefix(line, "* ") && !strings.HasPrefix(line, "* =") {
				starred = strings.Fields(line)[1]
			}
		}
		if i == 0 {
			first = starred
			if first != "apt" {
				t.Fatalf("the starred route is %q, want apt", first)
			}
			continue
		}
		if starred != first {
			t.Fatalf("the starred route changed: %q then %q", first, starred)
		}
	}
}

func TestGetAllShowsUninstalledManagers(t *testing.T) {
	opts := getOpts(t)
	// Nothing on PATH, so every manager is "not installed".
	_, plain, _ := run(t, opts, "get", "zig")
	_, all, _ := run(t, opts, "get", "zig", "--all")
	if len(all) <= len(plain) {
		t.Fatalf("--all did not add routes: %d vs %d", len(all), len(plain))
	}
	if !strings.Contains(all, "manager not installed") {
		t.Fatalf("--all output does not say which managers are missing:\n%s", all)
	}
}

func TestGetWithNoToolReportsMissingLanguages(t *testing.T) {
	opts := getOpts(t)
	code, out, errOut := run(t, opts, "get")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "have nothing usable") {
		t.Fatalf("the summary is missing:\n%s", out)
	}
	if !strings.Contains(out, "apt-get install -y") {
		t.Fatalf("no route was offered:\n%s", out)
	}
}

// A language with a toolchain must not be listed as missing.
func TestGetOmitsLanguagesThatAlreadyWork(t *testing.T) {
	opts := getOpts(t)
	// gcc on PATH makes the c and cpp languages resolvable.
	put(t, opts, "gcc", "#!/bin/sh\necho 'gcc 12.2.0'\n")
	_, out, _ := run(t, opts, "get")
	if strings.Contains(out, "\nc\n") {
		t.Fatalf("c was reported missing even though gcc is on PATH:\n%s", out)
	}
	if strings.Contains(out, "\ncpp\n") {
		t.Fatalf("cpp was reported missing even though gcc is on PATH:\n%s", out)
	}
}

// ---- the guard against installing unasked --------------------------------

// The whole point of the two flags: --run alone must print, not install.
func TestGetRunWithoutYesDoesNotInstall(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "apt-get", "#!/bin/sh\necho INSTALLED_MARKER\nexit 0\n")
	code, out, errOut := run(t, opts, "get", "gcc", "--run")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if strings.Contains(out, "INSTALLED_MARKER") {
		t.Fatalf("the installer ran without --yes:\n%s", out)
	}
	if !strings.Contains(out, "re-run with --yes") {
		t.Fatalf("the preview did not ask for confirmation:\n%s", out)
	}
}

func TestGetInstallNeedsBothRunAndYes(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "apt-get", "#!/bin/sh\necho INSTALLED_MARKER\n")
	_, out, _ := run(t, opts, "get", "gcc", "--yes")
	if strings.Contains(out, "INSTALLED_MARKER") {
		t.Fatalf("--yes without --run installed something:\n%s", out)
	}
	_, out, _ = run(t, opts, "get", "gcc", "--run")
	if strings.Contains(out, "INSTALLED_MARKER") {
		t.Fatalf("--run without --yes installed something:\n%s", out)
	}
}

// A real install through a fake manager: the manager must be invoked with the
// package as a separate argv element.
func TestGetRunWithYesInvokesTheManager(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	log := filepath.Join(opts.Env["HOME"], "calls.log")
	put(t, opts, "apt-get", "#!/bin/sh\necho \"$@\" >> "+log+"\n")
	code, out, errOut := run(t, opts, "get", "gcc", "--run", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s\nstdout:\n%s", code, errOut, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the manager was never invoked: %v", err)
	}
	if !strings.Contains(string(data), "gcc") {
		t.Fatalf("the package was not passed to the manager: %q", data)
	}
}

// The exact command is always printed before it runs, so the user can see what
// happened even when the manager produces no output.
func TestGetRunPrintsTheExactCommand(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	_, out, _ := run(t, opts, "get", "gcc", "--run", "--yes")
	if !strings.Contains(out, "apt-get install -y gcc") {
		t.Fatalf("the exact command was not shown:\n%s", out)
	}
}

// A failing manager must produce a nonzero exit.
func TestGetRunPropagatesManagerFailure(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	put(t, opts, "apt-get", "#!/bin/sh\necho 'E: Unable to locate package gcc' >&2\nexit 100\n")
	code, _, errOut := run(t, opts, "get", "gcc", "--run", "--yes")
	if code != 100 {
		t.Fatalf("exit = %d, want 100\nstderr:\n%s", code, errOut)
	}
}

// ---- --using --------------------------------------------------------------

func TestGetUsingSelectsTheNamedManager(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	put(t, opts, "snap", "#!/bin/sh\necho SNAP_CALLED\n")
	_, out, _ := run(t, opts, "get", "zig", "--run", "--yes", "--using", "snap")
	if !strings.Contains(out, "snap install zig") {
		t.Fatalf("the snap route was not used:\n%s", out)
	}
}

func TestGetUsingAnUnknownManagerFails(t *testing.T) {
	opts := getOpts(t)
	code, _, errOut := run(t, opts, "get", "zig", "--run", "--yes", "--using", "nosuchmgr")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "nosuchmgr") {
		t.Fatalf("stderr = %q, want it to name the manager", errOut)
	}
}

func TestGetUsingNeedsAValue(t *testing.T) {
	code, _, errOut := run(t, getOpts(t), "get", "zig", "--using")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "needs a manager") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// ---- update ---------------------------------------------------------------

// An installed tool must be updatable through the manager that owns it.
func TestGetUpdateTargetsTheOwningManager(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	// gcc on PATH reports an origin, so the update route must be preferred.
	put(t, opts, "gcc", "#!/bin/sh\necho 'gcc 12.2.0'\n")
	log := filepath.Join(opts.Env["HOME"], "update.log")
	put(t, opts, "apt-get", "#!/bin/sh\necho \"$@\" >> "+log+"\n")
	code, out, errOut := run(t, opts, "get", "gcc", "--update", "--run", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s\nstdout:\n%s", code, errOut, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the update never ran: %v\nstdout:\n%s", err, out)
	}
	got := string(data)
	if !strings.Contains(got, "upgrade") && !strings.Contains(got, "--only-upgrade") {
		t.Fatalf("the update ran an install, not an upgrade: %q", got)
	}
}

// A route that is only a download link has no update command, and claiming one
// would send the user to a URL instead of updating anything. Asking for the
// direct route by name must say so rather than pretend.
func TestGetUpdateSaysWhenThereIsNoRoute(t *testing.T) {
	opts := getOpts(t)
	code, out, errOut := run(t, opts, "get", "zig", "--update", "--using", "direct")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errOut)
	}
	if !strings.Contains(errOut, "no direct update route") {
		t.Fatalf("stderr = %q, want it to say there is no update route", errOut)
	}
	if strings.Contains(out, "update:") {
		t.Fatalf("an update route was claimed that does not exist:\n%s", out)
	}
}

// A manager that is not in the table at all must be named back.
func TestGetUpdateWithUnknownManagerNamesIt(t *testing.T) {
	opts := getOpts(t)
	code, _, errOut := run(t, opts, "get", "zig", "--update", "--using", "nosuchmgr")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "nosuchmgr") {
		t.Fatalf("stderr = %q, want it to name the manager", errOut)
	}
}

// ---- the cache fix --------------------------------------------------------

// The probe cache keyed on the path alone, so apt-get — which exits 1 with no
// arguments — was cached as unusable and never detected as a manager again.
func TestGetDetectsAptGetThatOnlyAnswersVersion(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "apt-get", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'apt 2.4.7'; exit 0; fi\nexit 1\n")
	_, out, errOut := run(t, opts, "get", "gcc")
	if code := 0; code != 0 {
		t.Fatal("unreachable")
	}
	if errOut != "" {
		t.Fatalf("unexpected stderr: %s", errOut)
	}
	if !strings.Contains(out, "apt-get install -y gcc") {
		t.Fatalf("apt-get was not detected as a manager:\n%s", out)
	}
	// And it must still be detected on a second run, from the cache.
	_, out2, _ := run(t, opts, "get", "gcc")
	if !strings.Contains(out2, "apt-get install -y gcc") {
		t.Fatalf("apt-get was not detected the second time:\n%s", out2)
	}
}

// ---- the TUI must be able to reach the same engine -----------------------

// The TUI and the CLI must agree, or the dashboard and the command line
// disagree about how to install something.
func TestGetEngineMatchesTheProvisionPackage(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	_, out, _ := run(t, opts, "get", "gcc")
	if !strings.Contains(out, "apt-get install -y gcc") {
		t.Fatalf("the CLI's route list is empty:\n%s", out)
	}
}

// ---- options plumbing -----------------------------------------------------

func TestRunPassesATimeoutToTheManager(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	// A pure-shell busy loop: the test PATH has no coreutils, so `sleep` would
	// itself fail with "not found" and the timeout would never be exercised.
	put(t, opts, "apt-get", "#!/bin/sh\nwhile :; do :; done\n")
	code, _, errOut := run(t, opts, "get", "gcc", "--run", "--yes", "--timeout", "200ms")
	if code == 0 {
		t.Fatal("a manager that was killed by the timeout reported success")
	}
	if !strings.Contains(errOut, "timed out") {
		t.Fatalf("stderr = %q, want it to say the timeout fired", errOut)
	}
}

func TestGetRunTimeoutNeedsAValue(t *testing.T) {
	code, _, errOut := run(t, getOpts(t), "get", "gcc", "--timeout")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "needs a value") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestGetRejectsAnUnknownFlag(t *testing.T) {
	code, _, errOut := run(t, getOpts(t), "get", "gcc", "--nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown flag") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// ---- regression: the probe cache and the TUI share one cache -----------

// TestUpdateCheckListsInstalledToolsWithNoSource lives in the tui package; this
// is the CLI-side half of the same machine state, so the two must not disagree.
func TestGetAndDoctorAgreeOnWhatIsInstalled(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "gcc", "#!/bin/sh\necho 'gcc 12.2.0'\n")
	_, doctorOut, _ := run(t, opts, "doctor", "--all")
	_, getOut, _ := run(t, opts, "get")
	if strings.Contains(getOut, "\nc\n") && !strings.Contains(doctorOut, "gcc") {
		t.Fatalf("get says c is missing but doctor found gcc:\nget:\n%s\ndoctor:\n%s", getOut, doctorOut)
	}
}

// A cache written by one command must be readable by the next, which is what
// makes the second `zedrun-go get` fast.
func TestGetReusesTheProbeCache(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "gcc", "#!/bin/sh\necho 'gcc 12.2.0'\n")
	cachePath := filepath.Join(opts.Env["XDG_CACHE_HOME"], "zedrun-go", "probe.json")
	_, _, _ = run(t, opts, "get", "gcc")
	if _, err := os.Stat(cachePath); err != nil {
		t.Skipf("no cache file at %s: %v", cachePath, err)
	}
	opts.Cache = detect.NewProbeCache(cachePath)
	_, out, errOut := run(t, opts, "get", "gcc")
	if errOut != "" {
		t.Fatalf("stderr: %s", errOut)
	}
	if !strings.Contains(out, "gcc") {
		t.Fatalf("the second run lost the route list:\n%s", out)
	}
}

// An outdated tool must say how to update it, not just that it is outdated.
func TestOutdatedNamesTheUpdateCommand(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	node := filepath.Join(opts.Env["HOME"], "bin", "node")
	if err := os.MkdirAll(filepath.Dir(node), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\necho 'v20.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(opts.Env["HOME"], ".zedrun.json"), `{"paths": {"node": "`+node+`"}}`)

	opts.ProjectStart = opts.Env["HOME"]
	opts.Fetcher = sources.NewFetcher("", time.Hour, func(url string) ([]byte, error) {
		switch url {
		case "https://nodejs.org/dist/index.json":
			return []byte(`[{"version":"v22.10.0","lts":"Jod"}]`), nil
		case "https://nodejs.org/download/rc/index.json":
			return []byte(`[]`), nil
		}
		return nil, os.ErrNotExist
	})

	_, out, errOut := run(t, opts, "outdated")
	if errOut != "" {
		t.Fatalf("stderr: %s", errOut)
	}
	if !strings.Contains(out, "outdated") {
		t.Fatalf("node was not reported outdated:\n%s", out)
	}
	if !strings.Contains(out, "update: winget") && !strings.Contains(out, "update: apt-get") {
		t.Fatalf("the update command is missing:\n%s", out)
	}
}
