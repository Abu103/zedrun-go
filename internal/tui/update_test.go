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

// wingetFixture renders a `winget list` table with the columns padded the way
// winget pads them, so the offsets the parser reads off the header line up with
// the rows. Rows are name|id|version|available.
func wingetFixture(rows ...string) string {
	widths := []int{34, 22, 12, 12}
	var b strings.Builder
	write := func(cells []string) {
		for i, c := range cells {
			if i == len(cells)-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(c)
			b.WriteString(strings.Repeat(" ", widths[i]-len(c)))
		}
		b.WriteString("\n")
	}
	write([]string{"Name", "Id", "Version", "Available", "Source"})
	total := widths[0] + widths[1] + widths[2] + widths[3] + len("Source")
	b.WriteString(strings.Repeat("-", total) + "\n")
	for _, r := range rows {
		f := strings.Split(r, "|")
		write([]string{f[0], f[1], f[2], f[3], "winget"})
	}
	return b.String()
}

// fakeWinget puts a winget shim on the test PATH that answers `winget list`
// from a fixed table.
//
// The shim cats the table by absolute path rather than through PATH: the test
// environment's PATH holds only the fakes the test wrote, so a bare `cat` would
// not be found and the shim would exit 127 — which reads as "winget will not
// answer" rather than as the table the test meant to serve.
func fakeWinget(t *testing.T, opts Options, table string) {
	t.Helper()
	dir := filepath.Join(opts.Env["HOME"], "bin")
	fixture := filepath.Join(dir, "winget-list.txt")
	if err := os.WriteFile(fixture, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	put(t, opts, "winget.exe", "#!/bin/sh\n/bin/cat "+fixture+"\n")
}

// go.dev has 1.27.3; winget's GoLang.Go manifest sits at 1.27.0. The check must
// say that rather than promise a version the command cannot install.
func TestUpdateCheckDoesNotPromiseWhatWingetCannotInstall(t *testing.T) {
	opts := updOpts(t)
	opts.OS = "windows"
	fakeTool(t, opts, "go.exe", "go version go1.27.0 windows/amd64")
	fakeWinget(t, opts, wingetFixture("Go Programming Language|GoLang.Go|1.27.0|"))

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "go.dev") {
			return []byte(`[{"version":"go1.27.3","stable":true}]`), nil
		}
		return nil, errors.New("offline")
	})

	out := strings.Join(pressUpdate(t, m).Output, "\n")
	if !strings.Contains(out, "nothing newer than 1.27.0") {
		t.Fatalf("did not say winget has nothing newer:\n%s", out)
	}
	if strings.Contains(out, "update: winget") {
		t.Fatalf("offered a command winget cannot honour:\n%s", out)
	}
}

// When winget does have a newer version, the hint names the version it will
// install — which can be older than upstream's.
func TestUpdateCheckNamesTheVersionWingetWillInstall(t *testing.T) {
	opts := updOpts(t)
	opts.OS = "windows"
	fakeTool(t, opts, "pwsh.exe", "7.6.6")
	fakeWinget(t, opts, wingetFixture("PowerShell 7-x64|Microsoft.PowerShell|7.6.6.0|7.6.7.0"))

	m := NewModel(rows(), config.Default(), opts)
	// pwsh's feed is the GitHub atom feed, so the fixture has to be atom: the
	// provider unmarshals XML and a JSON body is a parse error, not a version.
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "github.com/PowerShell") {
			return []byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>v7.6.7</title></entry>
  <entry><title>v7.6.6</title></entry>
</feed>`), nil
		}
		return nil, errors.New("offline")
	})

	out := strings.Join(pressUpdate(t, m).Output, "\n")
	if !strings.Contains(out, "update: winget upgrade --id Microsoft.PowerShell") {
		t.Fatalf("no winget command offered:\n%s", out)
	}
	if !strings.Contains(out, "→ 7.6.7.0") {
		t.Fatalf("did not name the version the command installs:\n%s", out)
	}
}

// A tool whose winget package is not installed must not be offered that
// command: it would install a second copy beside the first.
func TestUpdateCheckDropsAWingetPackageThatIsNotInstalled(t *testing.T) {
	opts := updOpts(t)
	opts.OS = "windows"
	fakeTool(t, opts, "node.exe", "v24.6.0")
	fakeWinget(t, opts, wingetFixture("Go|GoLang.Go|1.27.0|"))

	m := NewModel(rows(), config.Default(), opts)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(url string) ([]byte, error) {
		if strings.Contains(url, "nodejs.org") {
			return []byte(`[{"version":"v24.22.0"}]`), nil
		}
		return nil, errors.New("offline")
	})

	out := strings.Join(pressUpdate(t, m).Output, "\n")
	if strings.Contains(out, "update: winget") {
		t.Fatalf("offered winget for a package it does not have:\n%s", out)
	}
	if !strings.Contains(out, "no package manager on this machine owns node") {
		t.Fatalf("did not say nothing owns the tool:\n%s", out)
	}
	if strings.Contains(out, "second copy") {
		// the reason must explain the consequence, not just name it
	} else {
		t.Fatalf("did not explain what upgrading would do:\n%s", out)
	}
}
