package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zedrun/zedrun-go/internal/config"
)

func rows() []LangRow {
	return []LangRow{
		{Language: "c", Default: "gcc"},
		{Language: "python", Default: "python"},
		{Language: "zig", Default: "-"},
	}
}

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(t *testing.T, m Model, s string) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(key(s))
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return mm, cmd
}

func TestKeyActions(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	for _, tc := range []struct{ k, action string }{
		{"r", "run"},
		{"d", "doctor"},
		{"u", "updates"},
		{"t", "pick"},
		{"i", "install"},
	} {
		mm, _ := send(t, m, tc.k)
		if mm.LastAction != tc.action {
			t.Errorf("key %q set LastAction %q, want %q", tc.k, mm.LastAction, tc.action)
		}
	}
}

func TestQuitReturnsQuitCommand(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	mm, cmd := send(t, m, "q")
	if cmd == nil {
		t.Fatal("q returned no command, want tea.Quit")
	}
	if !mm.Quitting {
		t.Fatal("q did not mark the model as quitting")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q's command produced %T, want tea.QuitMsg", cmd())
	}
}

func TestCtrlCQuits(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := updated.(Model)
	if cmd == nil || !mm.Quitting {
		t.Fatal("ctrl+c did not quit")
	}
}

func TestPreToggle(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	if m.Pre {
		t.Fatal("Pre defaults to true, want false")
	}
	mm, _ := send(t, m, "p")
	if !mm.Pre {
		t.Fatal("p did not turn Pre on")
	}
	mm2, _ := send(t, mm, "p")
	if mm2.Pre {
		t.Fatal("p did not turn Pre off again")
	}
}

func TestCursorMovesAndClamps(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	if m.Cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.Cursor)
	}
	mm, _ := send(t, m, "down")
	if mm.Cursor != 1 {
		t.Fatalf("down → cursor %d, want 1", mm.Cursor)
	}
	mm, _ = send(t, mm, "up")
	mm, _ = send(t, mm, "up")
	if mm.Cursor != 0 {
		t.Fatalf("up past the top → cursor %d, want 0 (clamped)", mm.Cursor)
	}
	for i := 0; i < 10; i++ {
		mm, _ = send(t, mm, "down")
	}
	if mm.Cursor != len(mm.Rows)-1 {
		t.Fatalf("down past the bottom → cursor %d, want %d (clamped)", mm.Cursor, len(mm.Rows)-1)
	}
}

func TestLanguageTableReflectsRows(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	v := m.View()
	for _, want := range []string{"c", "gcc", "python", "zig"} {
		if !strings.Contains(v, want) {
			t.Errorf("View missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "zedrun-go") {
		t.Errorf("View has no title:\n%s", v)
	}
}

func TestKeyFooterListsKeys(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	v := m.View()
	for _, want := range []string{"run", "doctor", "updates", "pick", "install", "quit"} {
		if !strings.Contains(strings.ToLower(v), want) {
			t.Errorf("View footer missing %q:\n%s", want, v)
		}
	}
}

func TestActionOutputAccumulates(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	mm, _ := send(t, m, "u")
	if len(mm.Output) == 0 {
		t.Fatal("an action produced no output")
	}
	mm2, _ := send(t, mm, "d")
	if len(mm2.Output) <= len(mm.Output) {
		t.Fatal("output did not accumulate across actions")
	}
	if !strings.Contains(mm2.View(), mm2.Output[len(mm2.Output)-1]) {
		t.Fatal("the newest output line is not shown in the view")
	}
}

func TestWindowSizeIsStored(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	mm := updated.(Model)
	if mm.Width() != 100 || mm.Height() != 40 {
		t.Fatalf("size = (%d,%d), want (100,40)", mm.Width(), mm.Height())
	}
}
