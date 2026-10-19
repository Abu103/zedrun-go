package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// This file is an evidence harness for the dashboard defects written up in
// docs/BUG-REPORT.md. Each test prints the current behaviour and always passes,
// so the suite stays green while the bugs are open. As each one is fixed,
// convert the t.Logf into a real assertion — the logged numbers are the
// before-state to assert against.
//
// Run with -v to see the evidence:
//
//	go test ./internal/tui/ -run TestBug -v

func bugModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel([]LangRow{{Language: "c", Default: "gcc"}}, config.Default(), isolatedOpts(t))
	m.w, m.h = w, h
	return m
}

// BUG 1: View() is not windowed to the terminal height, so anything past the
// last visible row cannot be reached — and the alternate screen has no
// scrollback to fall back on. This is the "hijacked scroll".
func TestBug1ViewIsNotWindowedToTheTerminal(t *testing.T) {
	m := bugModel(t, 80, 24)
	for i := 0; i < 3; i++ {
		m.act("doctor")
	}
	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	t.Logf("terminal height %d -> View() emits %d lines (%d unreachable)",
		m.h, len(lines), len(lines)-m.h)
	visible := lines
	if len(visible) > m.h {
		visible = visible[:m.h]
	}
	t.Logf("key-hint footer visible in the first %d rows: %v",
		m.h, strings.Contains(strings.Join(visible, "\n"), "q quit"))
}

// BUG 2: the output pane is append-only. No cap, no clear, and no key removes
// anything, so repeated actions grow it without bound.
func TestBug2OutputPaneIsAppendOnly(t *testing.T) {
	m := bugModel(t, 80, 24)
	var sizes []int
	for i := 0; i < 5; i++ {
		m.act("doctor")
		sizes = append(sizes, len(m.Output))
	}
	t.Logf("Output length after each 'd': %v", sizes)
}

// BUG 3: no key scrolls the view.
func TestBug3NoScrollKeys(t *testing.T) {
	m := bugModel(t, 80, 24)
	m.act("doctor")
	before := m.View()
	for _, kt := range []tea.KeyType{
		tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd,
		tea.KeyCtrlU, tea.KeyCtrlD, tea.KeyCtrlB, tea.KeyCtrlF,
	} {
		updated, _ := m.Update(tea.KeyMsg{Type: kt})
		m = updated.(Model)
	}
	for _, r := range []string{"g", "G"} {
		m, _ = send(t, m, r)
	}
	t.Logf("pgup/pgdown/home/end/ctrl+u/ctrl+d/ctrl+b/ctrl+f/g/G changed the view: %v",
		m.View() != before)
}

// BUG 4: no mouse handling, so the wheel is ignored even though the alternate
// screen has already taken the terminal's own scrollback away.
func TestBug4NoMouseHandling(t *testing.T) {
	m := bugModel(t, 80, 24)
	before := m.View()
	for _, btn := range []tea.MouseButton{tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: btn})
		m = updated.(Model)
	}
	t.Logf("wheel events changed the view: %v", m.View() != before)
}

// BUG 5: long lines are not clamped to the terminal width, so they wrap and the
// layout below them shifts.
func TestBug5LinesAreNotClampedToTheWidth(t *testing.T) {
	m := bugModel(t, 80, 24)
	m.act("doctor")
	worst, worstLen := "", 0
	for _, l := range strings.Split(m.View(), "\n") {
		if len(l) > worstLen {
			worst, worstLen = l, len(l)
		}
	}
	t.Logf("terminal width %d -> widest rendered line %d", m.w, worstLen)
	t.Logf("worst: %s", worst)
}

// BUG 6: three advertised actions do not do what the key hints and README say.
func TestBug6AdvertisedActionsDoNotAct(t *testing.T) {
	for _, name := range []string{"run", "pick", "install"} {
		m := bugModel(t, 80, 24)
		m.act(name)
		t.Logf("act(%q) -> %s", name, strings.Join(m.Output, " | "))
	}
}

// Not a bug — recorded so the report is not accused of overclaiming. The
// dashboard starts fast even with a cold probe cache.
func TestBug7ColdScanIsFast(t *testing.T) {
	if os.Getenv("ZEDRUN_PROBE_REAL") == "" {
		t.Skip("set ZEDRUN_PROBE_REAL=1 to scan the real PATH")
	}
	start := time.Now()
	rows := Rows(config.Default(), Options{Cache: detect.NewProbeCache("")})
	t.Logf("cold scan of the real PATH: %v for %d languages", time.Since(start), len(rows))
}
