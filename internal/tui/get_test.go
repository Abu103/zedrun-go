package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// getOpts is isolatedOpts plus a PATH holding only the fakes the test writes, so
// the overlay cannot see the host's package managers.
func getOpts(t *testing.T) Options {
	t.Helper()
	opts := isolatedOpts(t)
	opts.Env["PATH"] = filepath.Join(opts.Env["HOME"], "bin")
	return opts
}

// put writes an executable into the test's PATH directory.
func put(t *testing.T, opts Options, name, body string) {
	t.Helper()
	dir := filepath.Join(opts.Env["HOME"], "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// putSudo puts a passwordless sudo shim on the test PATH.
func putSudo(t *testing.T, opts Options) {
	t.Helper()
	put(t, opts, "sudo", "#!/bin/sh\ncase \"$1\" in\n  -n) exit 0 ;;\nesac\nexec \"$@\"\n")
}

// The dashboard's own install command. It must be reachable with one key.
func TestGetKeyOpensTheInstallOverlay(t *testing.T) {
	m := NewModel(rows(), config.Default(), getOpts(t))
	mm, _ := send(t, m, "g")
	if mm.getMenu == nil {
		t.Fatal("g did not open the install overlay")
	}
	v := mm.View()
	for _, want := range []string{"get a toolchain", "nothing usable"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay missing %q:\n%s", want, v)
		}
	}
}

func TestGetOverlayEscCloses(t *testing.T) {
	m := NewModel(rows(), config.Default(), getOpts(t))
	mm, _ := send(t, m, "g")
	mm, _ = send(t, mm, "esc")
	if mm.getMenu != nil {
		t.Fatal("esc did not close the overlay")
	}
	if mm.Quitting {
		t.Fatal("esc closed the program instead of just the overlay")
	}
}

// Entering on a route must run it, not merely describe it.
func TestGetOverlayEnterRunsTheRoute(t *testing.T) {
	opts := getOpts(t)
	putSudo(t, opts)
	log := filepath.Join(opts.Env["HOME"], "calls.log")
	put(t, opts, "apt-get", "#!/bin/sh\necho \"$@\" >> "+log+"\n")

	m := NewModel(rows(), config.Default(), opts)
	mm, _ := send(t, m, "g")
	// The first row is the first missing language's route; pick it.
	if len(mm.getMenu.choices) == 0 {
		t.Fatal("the overlay offered nothing")
	}
	mm, cmd := send(t, mm, "enter")
	if mm.getMenu != nil {
		t.Fatal("enter did not close the overlay")
	}
	// The install is a command, so drive it to completion the way bubbletea
	// would: call it, then feed the message it returns back into the model.
	if cmd == nil {
		t.Fatal("enter returned no command, so the route was never run")
	}
	updated, _ := mm.Update(cmd())
	mm = updated.(Model)
	if len(mm.Output) == 0 {
		t.Fatal("running a route produced no output")
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "apt-get") {
		t.Fatalf("the route was not reported:\n%s", strings.Join(mm.Output, "\n"))
	}
}

// A route that cannot be elevated must be explained, not attempted.
func TestGetOverlayExplainsAnUnrunnableRoute(t *testing.T) {
	opts := getOpts(t)
	// No sudo shim, so no passwordless sudo.
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	m := NewModel(rows(), config.Default(), opts)
	mm, _ := send(t, m, "g")
	mm, cmd := send(t, mm, "enter")
	if cmd == nil {
		t.Fatal("enter returned no command")
	}
	updated, _ := mm.Update(cmd())
	mm = updated.(Model)
	joined := strings.Join(mm.Output, "\n")
	if !strings.Contains(joined, "sudo apt-get install") {
		t.Fatalf("the command to run by hand is missing:\n%s", joined)
	}
}

// The overlay must not offer a route for a language that already works.
func TestGetOverlaySkipsLanguagesThatWork(t *testing.T) {
	opts := getOpts(t)
	// gcc on PATH makes c and cpp resolvable.
	put(t, opts, "gcc", "#!/bin/sh\necho 'gcc 12.2.0'\n")
	m := NewModel(rows(), config.Default(), opts)
	mm, _ := send(t, m, "g")
	v := mm.View()
	if strings.Contains(v, "\nc\n") {
		t.Fatalf("c was offered even though gcc is on PATH:\n%s", v)
	}
}

// Every choice the overlay offers must carry a command, so enter always has
// something to do.
func TestGetOverlayChoicesAreRunnable(t *testing.T) {
	opts := getOpts(t)
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	m := NewModel(rows(), config.Default(), opts)
	mm, _ := send(t, m, "g")
	for _, c := range mm.getMenu.choices {
		if !c.method.Runnable() {
			t.Errorf("the overlay offered a non-runnable route: %+v", c)
		}
	}
}

// The footer must advertise the key, or it is undiscoverable.
func TestFooterAdvertisesTheInstallKey(t *testing.T) {
	m := NewModel(rows(), config.Default(), getOpts(t))
	v := strings.ToLower(m.View())
	if !strings.Contains(v, "get") {
		t.Fatalf("the footer does not mention get:\n%s", v)
	}
}

// The overlay is a terminal-mode screen, so it must not leak keys into the
// dashboard underneath while it is open.
func TestGetOverlaySwallowsKeys(t *testing.T) {
	m := NewModel(rows(), config.Default(), getOpts(t))
	mm, _ := send(t, m, "g")
	for _, k := range []string{"q", "c", "m"} {
		got, _ := send(t, mm, k)
		if got.Quitting {
			t.Fatalf("%q quit the program while the overlay was open", k)
		}
	}
	if mm.getMenu == nil {
		t.Fatal("the overlay was closed by a key it should have swallowed")
	}
}

// The overlay must survive a window resize.
func TestGetOverlaySurvivesResize(t *testing.T) {
	m := NewModel(rows(), config.Default(), getOpts(t))
	mm, _ := send(t, m, "g")
	updated, _ := mm.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	got := updated.(Model)
	if got.getMenu == nil {
		t.Fatal("a resize closed the overlay")
	}
	if got.Width() != 100 {
		t.Fatalf("width = %d, want 100", got.Width())
	}
}

// The overlay needs the probe cache the dashboard already holds.
func TestGetOverlayUsesTheSharedCache(t *testing.T) {
	opts := getOpts(t)
	cache := detect.NewProbeCache("")
	m := NewModel(rows(), config.Default(), Options{Cache: cache, Env: opts.Env, OS: opts.OS})
	mm, _ := send(t, m, "g")
	if mm.opts.Cache != cache {
		t.Fatal("the overlay did not keep the shared cache")
	}
}
