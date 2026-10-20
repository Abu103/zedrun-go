package provision

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

func fakeManager(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\necho \"" + name + " 1.0\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeEnv(dir string) map[string]string {
	return map[string]string{"PATH": dir, "HOME": dir}
}

// The guard against a new tool shipping with no way to install it.
func TestEveryCatalogToolHasARoute(t *testing.T) {
	for id := range catalog.Tools {
		for _, osName := range []string{"windows", "linux"} {
			if osName == "linux" && windowsOnly[id] {
				continue
			}
			ms := Available(id, osName, map[string]string{}, true)
			if len(ms) == 0 {
				t.Errorf("%s has no route on %s", id, osName)
			}
		}
	}
}

func TestDetectedManagersFromAPathEntry(t *testing.T) {
	dir := t.TempDir()
	// Windows executables need an extension: ExeExts defaults to
	// .COM;.EXE;.BAT;.CMD, so a bare "winget" is never found there.
	fakeManager(t, dir, "winget.cmd")
	fakeManager(t, dir, "apt-get")

	got := Detected(config.Default(), detect.NewProbeCache(""), fakeEnv(dir), "windows")
	if _, ok := got["winget"]; !ok {
		t.Errorf("winget was not detected from PATH: %v", got)
	}
	if _, ok := got["apt"]; ok {
		t.Error("apt must not be detected on windows")
	}

	got = Detected(config.Default(), detect.NewProbeCache(""), fakeEnv(dir), "linux")
	if _, ok := got["apt"]; !ok {
		t.Errorf("apt-get was not detected as apt: %v", got)
	}
	if _, ok := got["winget"]; ok {
		t.Error("winget must not be detected on linux")
	}
}

func TestAvailableHidesManagersThatAreNotInstalled(t *testing.T) {
	withAll := Available("zig", "linux", map[string]string{}, true)
	without := Available("zig", "linux", map[string]string{}, false)
	if len(without) >= len(withAll) {
		t.Errorf("filtering removed nothing: %d vs %d", len(without), len(withAll))
	}
	// A direct download survives filtering: it needs no manager.
	var sawDirect bool
	for _, m := range without {
		if m.Manager == "direct" {
			sawDirect = true
		}
	}
	if !sawDirect {
		t.Error("the direct route was filtered out, but it needs no manager")
	}
}

// A package name must never become shell syntax.
func TestArgvIsNotAShellString(t *testing.T) {
	ms := Available("gcc", "linux", map[string]string{"apt": "/usr/bin/apt-get"}, false)
	if len(ms) == 0 {
		t.Fatal("no apt route for gcc")
	}
	m := ms[0]
	if m.Argv[0] == "sh" || m.Argv[0] == "bash" {
		t.Errorf("the route goes through a shell: %v", m.Argv)
	}
	// Every element is a separate argument, so a name with a space stays one.
	got := substitute([]string{"install", "{package}"}, "a b; rm -rf /")
	if len(got) != 2 || got[1] != "a b; rm -rf /" {
		t.Errorf("substitute mangled the argument: %#v", got)
	}
}

func TestOrderPutsTheOwningManagerFirst(t *testing.T) {
	ms := []Method{{Manager: "winget"}, {Manager: "scoop"}, {Manager: "choco"}}
	got := Order(ms, "choco")
	if got[0].Manager != "choco" {
		t.Errorf("owner not moved to the front: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("Order dropped entries: %v", got)
	}
	if Order(ms, "")[0].Manager != "winget" {
		t.Error("an unknown owner must leave the order alone")
	}
}

func TestRenderSaysWhenThereIsNothing(t *testing.T) {
	var b bytes.Buffer
	Render(&b, "nosuchtool", nil)
	if !strings.Contains(b.String(), "no way to install") {
		t.Errorf("Render = %q", b.String())
	}
}

// A fake manager that exits 3 must have that code propagated.
func TestRunPropagatesTheExitCode(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "manager")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run(Method{Tool: "x", Manager: "m", Argv: []string{script, "install"}},
		RunOptions{Stdout: &out, Stderr: &errOut})
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "$ ") {
		t.Errorf("the exact command was not printed first: %q", out.String())
	}
}

// Elevating without being asked is the thing this must never do.
func TestRunRefusesToElevateUnasked(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(Method{Tool: "gcc", Manager: "apt", Argv: []string{"apt-get", "install", "-y", "gcc"}, Elevated: true},
		RunOptions{Stdout: &out, Stderr: &errOut})
	if code == 0 {
		t.Error("an elevated method ran without permission")
	}
	if !strings.Contains(errOut.String(), "will not elevate") {
		t.Errorf("no explanation: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "sudo apt-get install -y gcc") {
		t.Errorf("the command to run by hand is missing: %q", errOut.String())
	}
}

func TestRunRefusesADirectMethod(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(Method{Tool: "zig", Manager: "direct", URL: "https://ziglang.org/download/"},
		RunOptions{Stdout: &out, Stderr: &errOut})
	if code == 0 {
		t.Error("a direct method claimed to have run")
	}
	if !strings.Contains(errOut.String(), "ziglang.org") {
		t.Errorf("the URL is missing: %q", errOut.String())
	}
}

// ---- Available order ------------------------------------------------------

// Available used to range over the manager table in map order, so the list it
// returned moved between runs and the starred "--run" choice changed with it.
func TestAvailableOrderIsStableAcrossCalls(t *testing.T) {
	var first []string
	for i := 0; i < 25; i++ {
		ms := Available("gcc", "linux", map[string]string{}, true)
		var got []string
		for _, m := range ms {
			got = append(got, m.Manager)
		}
		if first == nil {
			first = got
			continue
		}
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("the order changed between calls:\nfirst: %v\nnow:   %v", first, got)
		}
	}
}

func TestAvailablePrefersTheDistroManagerOverLanguageOnes(t *testing.T) {
	ms := Available("rustc", "linux", map[string]string{}, true)
	if len(ms) < 2 {
		t.Fatalf("expected more than one route for rustc, got %v", ms)
	}
	if ms[0].Manager != "apt" {
		t.Errorf("the first route is %s, want apt — the distro manager should win", ms[0].Manager)
	}
}

// ---- windowsOnly ---------------------------------------------------------

func TestWindowsOnlyToolsHaveNoLinuxRoute(t *testing.T) {
	for id := range windowsOnly {
		if ms := Available(id, "linux", map[string]string{}, true); len(ms) != 0 {
			t.Errorf("%s is windows-only but offered a linux route: %v", id, ms)
		}
	}
}

// ---- substitute ----------------------------------------------------------

func TestSubstituteLeavesATemplateWithoutTheMarkerAlone(t *testing.T) {
	got := substitute([]string{"winget", "install", "--id", "{package}"}, "GoLang.Go")
	if strings.Join(got, " ") != "winget install --id GoLang.Go" {
		t.Errorf("substitute = %v", got)
	}
	if got := substitute([]string{"echo", "no marker here"}, "GoLang.Go"); len(got) != 2 {
		t.Errorf("substitute added an element: %v", got)
	}
}

// ---- Render --------------------------------------------------------------

func TestRenderMarksOnlyTheRunnableChoice(t *testing.T) {
	var b bytes.Buffer
	Render(&b, "julia", []Method{
		{Manager: "winget", Argv: []string{"winget", "install", "x"}, Present: true},
		{Manager: "direct", URL: "https://example.com"},
	})
	lines := strings.Split(b.String(), "\n")
	if len(lines) < 4 {
		t.Fatalf("Render produced too little: %q", b.String())
	}
	if !strings.HasPrefix(lines[2], "* ") {
		t.Errorf("the runnable route was not starred: %q", lines[2])
	}
	if strings.HasPrefix(lines[3], "* ") {
		t.Errorf("the direct route must not be starred: %q", lines[3])
	}
}

// ---- exec injection surface ----------------------------------------------

// The argv comes from the table, so nothing a user typed may add an element.
func TestPackageNamesNeverAddArgvElements(t *testing.T) {
	evil := "golang-go; touch /tmp/pwned"
	got := substitute([]string{"apt-get", "install", "-y", "{package}"}, evil)
	if len(got) != 4 {
		t.Fatalf("the package name added an element: %v", got)
	}
	if got[3] != evil {
		t.Errorf("the package name was mangled: %q", got[3])
	}
}

// ---- Run with a real exec failure ---------------------------------------

func TestRunReportsAMissingExecutable(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(Method{Tool: "x", Manager: "m", Argv: []string{"/nonexistent/manager-xyz", "install"}},
		RunOptions{Stdout: &out, Stderr: &errOut})
	if code != -1 {
		t.Errorf("exit = %d, want -1 for a command that could not start", code)
	}
	if !strings.Contains(errOut.String(), "could not run") {
		t.Errorf("no explanation: %q", errOut.String())
	}
}

// The update command comes from the manager table, so it is a real argv too.
func TestManagersCarryAnUpdateCommand(t *testing.T) {
	for _, m := range Known {
		if len(m.Update) == 0 {
			t.Errorf("%s has no update command", m.Name)
			continue
		}
		// Every install/update template must carry the marker, otherwise
		// substitute silently produces an install command that names nothing.
		hasMarker := false
		for _, a := range m.Update {
			if strings.Contains(a, "{package}") {
				hasMarker = true
			}
		}
		if !hasMarker {
			t.Errorf("%s update template has no {package} marker: %v", m.Name, m.Update)
		}
	}
}

// Direct routes must carry a URL and a note, or the user is told nothing.
func TestDirectRoutesAreActionable(t *testing.T) {
	for id, d := range directURL {
		if d.URL == "" {
			t.Errorf("the direct route for %s has no URL", id)
		}
		if d.Note == "" {
			t.Errorf("the direct route for %s has no note", id)
		}
	}
}

// A tool with no package anywhere must still have a route, or the
// every-tool-has-a-route guard would be vacuous.
func TestRoutelessToolsHaveADirectRoute(t *testing.T) {
	for id := range catalog.Tools {
		if len(packages[id]) == 0 {
			if _, ok := directURL[id]; !ok {
				t.Errorf("%s has neither a package nor a direct route", id)
			}
		}
	}
}
