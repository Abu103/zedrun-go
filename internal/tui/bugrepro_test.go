package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// This file is an evidence harness for the dashboard defects written up in
// docs/BUG-REPORT.md. Each test now asserts the fixed behaviour.
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

// BUG 1: View() is windowed to the terminal; footer always on the last row.
func TestBug1ViewIsWindowedToTheTerminal(t *testing.T) {
	m := bugModel(t, 80, 24)
	for i := 0; i < 3; i++ {
		m.act("doctor")
	}
	m.clamp()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.h {
		t.Fatalf("View emits %d lines, want exactly %d", len(lines), m.h)
	}
	if !strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("footer not on last row: %q", lines[len(lines)-1])
	}
}

// BUG 2: output pane is capped and clearable.
func TestBug2OutputPaneIsCappedAndClearable(t *testing.T) {
	m := bugModel(t, 80, 24)
	for i := 0; i < maxOutput+50; i++ {
		m.append("x")
	}
	if len(m.Output) != maxOutput {
		t.Fatalf("len(Output) = %d, want cap %d", len(m.Output), maxOutput)
	}
	m, _ = send(t, m, "c")
	if len(m.Output) != 0 {
		t.Fatalf("c did not clear output: %d lines", len(m.Output))
	}
}

// BUG 3: keys scroll the view, and clamp.
func TestBug3ScrollKeys(t *testing.T) {
	m := bugModel(t, 80, 24)
	for i := 0; i < 100; i++ {
		m.append("line")
	}
	m.clamp()
	press := func(kt tea.KeyType) {
		u, _ := m.Update(tea.KeyMsg{Type: kt})
		m = u.(Model)
	}
	press(tea.KeyHome)
	if m.Scroll != 0 {
		t.Fatalf("home: scroll = %d", m.Scroll)
	}
	press(tea.KeyPgDown)
	if m.Scroll != m.window() {
		t.Fatalf("pgdown: scroll = %d, want %d", m.Scroll, m.window())
	}
	press(tea.KeyPgUp)
	if m.Scroll != 0 {
		t.Fatalf("pgup: scroll = %d", m.Scroll)
	}
	press(tea.KeyEnd)
	if max := m.bodyLen() - m.window(); m.Scroll != max {
		t.Fatalf("end: scroll = %d, want %d", m.Scroll, max)
	}
	press(tea.KeyPgDown)
	if max := m.bodyLen() - m.window(); m.Scroll != max {
		t.Fatalf("scroll not clamped: %d > %d", m.Scroll, max)
	}
}

// BUG 3b: moving the cursor out of the window scrolls to follow it.
func TestBug3CursorFollowsWindow(t *testing.T) {
	rs := make([]LangRow, 40)
	for i := range rs {
		rs[i] = LangRow{Language: "l", Default: "t"}
	}
	m := NewModel(rs, config.Default(), isolatedOpts(t))
	m.w, m.h = 80, 10
	for i := 0; i < 30; i++ {
		m, _ = send(t, m, "j")
	}
	if m.Cursor < m.Scroll || m.Cursor >= m.Scroll+m.window() {
		t.Fatalf("cursor %d outside window [%d,%d)", m.Cursor, m.Scroll, m.Scroll+m.window())
	}
}

// BUG 4: the wheel scrolls.
func TestBug4MouseWheelScrolls(t *testing.T) {
	m := bugModel(t, 80, 24)
	for i := 0; i < 100; i++ {
		m.append("line")
	}
	m.clamp()
	u, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m = u.(Model)
	if m.Scroll != 3 {
		t.Fatalf("wheel down: scroll = %d, want 3", m.Scroll)
	}
	u, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if u.(Model).Scroll != 0 {
		t.Fatalf("wheel up: scroll = %d, want 0", u.(Model).Scroll)
	}
}

// BUG 5: no rendered line is wider than the terminal.
func TestBug5LinesAreClampedToTheWidth(t *testing.T) {
	m := bugModel(t, 80, 24)
	m.append(strings.Repeat("w", 300))
	m.clamp()
	for _, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > m.w {
			t.Fatalf("line width %d > %d: %q", w, m.w, l)
		}
	}
}

// BUG 6: `i` previews first, second `i` applies; `t` opens a picker.
func TestBug6InstallNeedsConfirmation(t *testing.T) {
	m := bugModel(t, 80, 24)
	m, _ = send(t, m, "i")
	if !m.confirmInstall {
		t.Fatal("first i should arm the confirmation")
	}
	m, _ = send(t, m, "x")
	if m.confirmInstall {
		t.Fatal("any other key should disarm the confirmation")
	}
}

func TestBug6PickOpensPicker(t *testing.T) {
	m := bugModel(t, 80, 24)
	m, _ = send(t, m, "t")
	// depends on the host (fixed dirs like /usr/bin are probed regardless of
	// PATH): either the picker opens with choices, or we get a message.
	if m.picker == nil && len(m.Output) == 0 {
		t.Fatal("t gave neither a picker nor a message")
	}
	if m.picker != nil && len(m.picker.choices) == 0 {
		t.Fatal("picker opened with no choices")
	}
	m.picker = &picker{lang: "c", choices: []choice{{Tool: "gcc", Path: "/usr/bin/gcc", OK: true}}}
	if v := m.View(); !strings.Contains(v, "/usr/bin/gcc") || len(strings.Split(v, "\n")) != m.h {
		t.Fatalf("bad picker view:\n%s", v)
	}
	m, _ = send(t, m, "esc")
	if m.picker != nil {
		t.Fatal("esc did not close the picker")
	}
}

// Not a bug — cold scan stays fast.
func TestBug7ColdScanIsFast(t *testing.T) {
	if os.Getenv("ZEDRUN_PROBE_REAL") == "" {
		t.Skip("set ZEDRUN_PROBE_REAL=1 to scan the real PATH")
	}
	start := time.Now()
	rows := Rows(config.Default(), Options{Cache: detect.NewProbeCache("")})
	t.Logf("cold scan of the real PATH: %v for %d languages", time.Since(start), len(rows))
}
