package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/zedrun/zedrun-go/internal/provision"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// statusRow is the status bar: the third line of every dashboard screen.
func statusRow(m Model) string { return strings.Split(m.View(), "\n")[2] }

func TestStatusBarNamesTheToolchainAndTheOutdatedCount(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.Fetcher = sources.NewFetcher("", time.Minute, func(string) ([]byte, error) {
		return nil, errors.New("offline")
	})

	if got := statusRow(m); !strings.Contains(got, "c → gcc") || !strings.Contains(got, "not checked") {
		t.Fatalf("before a check the bar should name the toolchain and say it is unchecked: %q", got)
	}

	u, _ := m.Update(updatesMsg{lines: []string{"x"}, upd: []updChoice{{tool: "python"}}, outdated: 3})
	m = u.(Model)
	if got := statusRow(m); !strings.Contains(got, "3 outdated") || !strings.Contains(got, "U updates") {
		t.Fatalf("outdated count missing from the bar: %q", got)
	}

	// Outdated tools with no runnable route are still counted, but U is not
	// advertised for them.
	u, _ = m.Update(updatesMsg{lines: []string{"x"}, outdated: 2})
	m = u.(Model)
	if got := statusRow(m); !strings.Contains(got, "2 outdated") || strings.Contains(got, "U updates") {
		t.Fatalf("a tool with no route should not advertise U: %q", got)
	}

	u, _ = m.Update(updatesMsg{lines: []string{"x"}})
	m = u.(Model)
	if got := statusRow(m); !strings.Contains(got, "all up to date") {
		t.Fatalf("nothing outdated should say so: %q", got)
	}

	// A finished update makes the old count stale.
	u, _ = m.Update(updDoneMsg{"update: done"})
	m = u.(Model)
	if got := statusRow(m); !strings.Contains(got, "not checked") {
		t.Fatalf("the bar still claims a result after an update ran: %q", got)
	}
}

func TestStatusBarDoesNotClaimAnUncheckedResult(t *testing.T) {
	m := actionModel(t, 80, 24) // no Fetcher: nothing can be compared
	u, _ := m.Update(updatesMsg{lines: []string{"updates: no version source configured"}})
	m = u.(Model)
	if got := statusRow(m); strings.Contains(got, "all up to date") {
		t.Fatalf("with no version source the bar says everything is up to date: %q", got)
	}
}

func TestStatusBarFlagsALanguageWithNoToolchain(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.Cursor = 2 // zig has no default
	if got := statusRow(m); !strings.Contains(got, "zig: no toolchain") {
		t.Fatalf("bar does not flag the missing toolchain: %q", got)
	}
}

func updFixture(n int) *updMenu {
	var cs []updChoice
	for i := 0; i < n; i++ {
		cs = append(cs, updChoice{
			tool: fmt.Sprintf("t%d", i), installed: "1.0.0", latest: "1.2.0", achievable: "1.1.0",
			method: provision.Method{Manager: "apt", Argv: []string{"apt-get", "install", "--only-upgrade", fmt.Sprintf("pkg%d", i)}},
		})
	}
	return &updMenu{choices: cs}
}

// The overlay used to replace the whole screen. It must now sit inside the
// dashboard: header, status bar and footer stay, and the screen is exactly as
// tall as the terminal.
func TestUpdateOverlayKeepsTheDashboardFrame(t *testing.T) {
	m := actionModel(t, 60, 12)
	m.updMenu = updFixture(2)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.h {
		t.Fatalf("overlay is %d lines, want %d", len(lines), m.h)
	}
	if !strings.Contains(lines[0], "zedrun-go") {
		t.Errorf("header missing: %q", lines[0])
	}
	if !strings.Contains(lines[2], "c → gcc") {
		t.Errorf("status bar missing: %q", lines[2])
	}
	if !strings.Contains(lines[len(lines)-1], "esc cancel") {
		t.Errorf("overlay footer missing: %q", lines[len(lines)-1])
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > m.w {
			t.Errorf("line is %d wide, terminal is %d: %q", w, m.w, l)
		}
	}
}

// The "→ next (upstream Y)" note and the end of a long command are what the
// user needs; neither may be truncated.
func TestUpdateOverlayWrapsInsteadOfTruncating(t *testing.T) {
	m := actionModel(t, 50, 14)
	menu := updFixture(1)
	menu.choices[0].latest = "1.9.0"
	menu.choices[0].method.Argv = strings.Fields("apt-get install --only-upgrade alpha beta gamma delta epsilon zeta")
	m.updMenu = menu
	v := m.View()
	for _, want := range []string{"1.1.0", "upstream 1.9.0", "epsilon", "zeta"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay lost %q:\n%s", want, v)
		}
	}
	for _, l := range strings.Split(v, "\n") {
		if w := lipgloss.Width(l); w > m.w {
			t.Errorf("line is %d wide, terminal is %d: %q", w, m.w, l)
		}
	}
}

// With more outdated tools than rows, the highlighted block must stay on
// screen and the screen must not grow.
func TestUpdateOverlayScrollsToTheCursor(t *testing.T) {
	m := actionModel(t, 60, 10)
	m.updMenu = updFixture(10)
	m.updMenu.cursor = 9
	v := m.View()
	if got := len(strings.Split(v, "\n")); got != m.h {
		t.Fatalf("overlay is %d lines, want %d", got, m.h)
	}
	if !strings.Contains(v, "t9") || !strings.Contains(v, "pkg9") {
		t.Fatalf("the highlighted tool is off screen:\n%s", v)
	}
}

// Every key the footer advertises must exist, and the hint must be picked to
// fit rather than cut off mid-word.
func TestFooterFitsTheWidthAndKeepsTheKeys(t *testing.T) {
	for _, w := range []int{80, 100, 120, 200} {
		f := footerFor(w)
		if lipgloss.Width(f) > w {
			t.Errorf("footer is %d wide at width %d: %q", lipgloss.Width(f), w, f)
		}
		for _, want := range []string{"r run", "d doctor", "u updates", "U update", "t pick", "g get", "i zed", "q quit"} {
			if !strings.Contains(f, want) {
				t.Errorf("footer at width %d lost %q: %q", w, want, f)
			}
		}
	}
	if f := footerFor(120); !strings.Contains(f, "m mouse") || !strings.Contains(f, "c clear") {
		t.Errorf("a wide footer should list m and c: %q", f)
	}
}
