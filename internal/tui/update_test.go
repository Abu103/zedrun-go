package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// updOpts is isolatedOpts with a PATH holding only the fakes the test writes.
func updOpts(t *testing.T) Options {
	t.Helper()
	opts := isolatedOpts(t)
	opts.Env["PATH"] = filepath.Join(opts.Env["HOME"], "bin")
	return opts
}

// fakeTool writes an executable that reports a fixed version.
func fakeTool(t *testing.T, opts Options, name, version string) {
	t.Helper()
	dir := filepath.Join(opts.Env["HOME"], "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho '" + version + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The dashboard used to print one line per install, so four bash installs made
// four identical rows. A tool is one row.
func TestUpdateCheckListsEachToolOnce(t *testing.T) {
	opts := updOpts(t)
	for _, d := range []string{"a", "b", "c", "d"} {
		dir := filepath.Join(opts.Env["HOME"], d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/sh\necho 'GNU bash, version 5.2.21'\n"
		if err := os.WriteFile(filepath.Join(dir, "bash"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	opts.Env["PATH"] = strings.Join([]string{
		filepath.Join(opts.Env["HOME"], "a"),
		filepath.Join(opts.Env["HOME"], "b"),
		filepath.Join(opts.Env["HOME"], "c"),
		filepath.Join(opts.Env["HOME"], "d"),
	}, string(os.PathListSeparator))

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute,
		func(string) ([]byte, error) { return nil, errors.New("offline") })

	got := strings.Join(m.updateLines(), "\n")
	n := strings.Count(got, "bash")
	if n != 1 {
		t.Fatalf("bash appeared %d times, want 1:\n%s", n, got)
	}
}

// An outdated tool must say how to update it, not just that it is outdated.
func TestUpdateCheckNamesTheUpdateCommand(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		return nil, errors.New("offline")
	})
	// The python feed is the one thing this test needs; everything else fails
	// and is reported as such, which is fine.
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.13.7/">3.13.7/</a><a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})

	got := strings.Join(m.updateLines(), "\n")
	if !strings.Contains(got, "outdated") {
		t.Fatalf("python was not reported as outdated:\n%s", got)
	}
	if !strings.Contains(got, "apt-get install --only-upgrade") {
		t.Fatalf("the update command is missing:\n%s", got)
	}
}

// ---- the U key: update the outdated ones ---------------------------------

func TestUpdateKeyOpensTheUpdateOverlay(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})

	mm := pressUpdate(t, m)
	if mm.updMenu == nil {
		t.Fatal("U did not open the update overlay")
	}
	v := mm.View()
	for _, want := range []string{"update toolchains", "python", "apt-get install --only-upgrade"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay missing %q:\n%s", want, v)
		}
	}
}

// With nothing outdated there is nothing to offer, and saying so beats an
// empty menu.
func TestUpdateKeySaysWhenEverythingIsCurrent(t *testing.T) {
	opts := updOpts(t)
	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute,
		func(string) ([]byte, error) { return nil, errors.New("offline") })

	mm := pressUpdate(t, m)
	if mm.updMenu != nil {
		t.Fatal("U opened an overlay with nothing to update")
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "up to date") {
		t.Fatalf("no explanation:\n%s", strings.Join(mm.Output, "\n"))
	}
}

func TestUpdateOverlayEscCloses(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})
	mm := pressUpdate(t, m)
	mm, _ = send(t, mm, "esc")
	if mm.updMenu != nil {
		t.Fatal("esc did not close the update overlay")
	}
	if mm.Quitting {
		t.Fatal("esc quit the program instead of closing the overlay")
	}
}

// Entering on an update runs it, in the background like the install overlay.
func TestUpdateOverlayEnterRunsTheUpdate(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	putSudo(t, opts)
	log := filepath.Join(opts.Env["HOME"], "update.log")
	put(t, opts, "apt-get", "#!/bin/sh\necho \"$@\" >> "+log+"\n")

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})

	mm := pressUpdate(t, m)
	if len(mm.updMenu.choices) == 0 {
		t.Fatal("the overlay offered nothing")
	}
	mm, cmd := send(t, mm, "enter")
	if cmd == nil {
		t.Fatal("enter returned no command, so nothing was updated")
	}
	updated, _ := mm.Update(cmd())
	mm = updated.(Model)

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the update never ran: %v\noutput:\n%s", err, strings.Join(mm.Output, "\n"))
	}
	if !strings.Contains(string(data), "--only-upgrade") {
		t.Fatalf("an install ran instead of an upgrade: %q", data)
	}
}

// The overlay takes every key while it is open.
func TestUpdateOverlaySwallowsKeys(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})
	mm := pressUpdate(t, m)
	// Non-overlay keys must not quit the program or fall through to the
	// dashboard underneath: a stray q must not lose the scrollback.
	for _, k := range []string{"c", "m", "g", "d", "r"} {
		got, _ := send(t, mm, k)
		if got.Quitting {
			t.Fatalf("%q quit the program while the overlay was open", k)
		}
		if got.updMenu == nil {
			t.Fatalf("%q closed the overlay, which should swallow it", k)
		}
		if got.LastAction != "update" {
			t.Fatalf("%q leaked through to the dashboard (LastAction=%q)", k, got.LastAction)
		}
	}
	// q closes the overlay — that is what an overlay taking every key means —
	// but it must not quit the program.
	got, _ := send(t, mm, "q")
	if got.Quitting {
		t.Fatal("q quit the program instead of closing the overlay")
	}
	if got.updMenu != nil {
		t.Fatal("q did not close the overlay")
	}
}

// The footer must advertise the update key.
func TestFooterAdvertisesTheUpdateKey(t *testing.T) {
	m := NewModel(rows(), config.Default(), updOpts(t))
	if !strings.Contains(strings.ToLower(m.View()), "update") {
		t.Fatalf("the footer does not mention update:\n%s", m.View())
	}
}

// The update overlay must use the cache the dashboard already holds, or it
// re-probes every tool from scratch.
func TestUpdateOverlayUsesTheSharedCache(t *testing.T) {
	opts := updOpts(t)
	fakeTool(t, opts, "python3", "Python 3.13.7")
	put(t, opts, "apt-get", "#!/bin/sh\nexit 0\n")
	m := NewModel(rows(), config.Default(), Options{Cache: detect.NewProbeCache(""), Env: opts.Env, OS: opts.OS})
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "python.org") {
			return []byte(`<a href="3.16.0/">3.16.0/</a>`), nil
		}
		return nil, errors.New("offline")
	})
	mm, _ := send(t, m, "U")
	if mm.opts.Cache == nil {
		t.Fatal("the update overlay dropped the shared cache")
	}
}

// pressUpdate presses U and drives the update check to completion: the key
// returns a command, and feeding its message back is what opens the overlay.
func pressUpdate(t *testing.T, m Model) Model {
	t.Helper()
	mm, cmd := send(t, m, "U")
	if cmd == nil {
		t.Fatal("U returned no command, so no check was started")
	}
	updated, _ := mm.Update(cmd())
	return updated.(Model)
}
