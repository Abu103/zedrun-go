package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// flatten runs a command and returns every message it yields, unpacking a
// batch the way bubbletea would.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, flatten(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func hasClear(msgs []tea.Msg) bool {
	want := reflect.TypeOf(tea.ClearScreen())
	for _, m := range msgs {
		if reflect.TypeOf(m) == want {
			return true
		}
	}
	return false
}

// A new action replaces the last one's output: one page per action.
func TestEachActionReplacesTheLastOutput(t *testing.T) {
	m := actionModel(t, 80, 24)
	m, _ = send(t, m, "p")
	if got := strings.Join(m.Output, "\n"); !strings.Contains(got, "channel: pre") {
		t.Fatalf("p did not report the channel: %q", got)
	}
	m, _ = send(t, m, "p")
	got := strings.Join(m.Output, "\n")
	if strings.Contains(got, "channel: pre") || !strings.Contains(got, "channel: stable") {
		t.Fatalf("the second action did not replace the first: %q", got)
	}
	if len(m.Output) != 1 {
		t.Fatalf("output is %d lines, want exactly the one action's line: %v", len(m.Output), m.Output)
	}
}

// Scrolling is not an action; it must keep what is on screen.
func TestNavigationKeepsTheOutput(t *testing.T) {
	m := actionModel(t, 80, 24)
	m, _ = send(t, m, "p")
	m, _ = send(t, m, "j")
	if len(m.Output) != 1 {
		t.Fatalf("a navigation key changed the output: %v", m.Output)
	}
}

// Every key press clears the terminal before the redraw.
func TestKeyPressClearsTheScreen(t *testing.T) {
	var tm tea.Model = clearing{actionModel(t, 80, 24)}
	_, cmd := tm.Update(key("p"))
	if !hasClear(flatten(cmd)) {
		t.Fatal("a key press did not ask for a screen clear")
	}
}

// A window resize is not a key; it must not force a clear.
func TestResizeDoesNotClear(t *testing.T) {
	var tm tea.Model = clearing{actionModel(t, 80, 24)}
	_, cmd := tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if hasClear(flatten(cmd)) {
		t.Fatal("a resize forced a clear")
	}
}

// The key that quits must not queue a clear behind the exit.
func TestQuitDoesNotClear(t *testing.T) {
	var tm tea.Model = clearing{actionModel(t, 80, 24)}
	_, cmd := tm.Update(key("q"))
	if hasClear(flatten(cmd)) {
		t.Fatal("quit also asked for a clear")
	}
}

// The wrapper must still be a complete tea.Model whose View is the model's.
func TestClearingViewIsTheModelView(t *testing.T) {
	m := actionModel(t, 80, 24)
	var tm tea.Model = clearing{m}
	if tm.View() != m.View() {
		t.Fatal("the wrapper changed what is drawn")
	}
}

// Output is padded on the left and never wider than the terminal.
func TestOutputIsPaddedAndFits(t *testing.T) {
	m := actionModel(t, 40, 24)
	m.append("a fairly long output line that has to wrap at forty columns")
	n := 0
	for _, l := range m.body() {
		if strings.Contains(l, "forty") || strings.Contains(l, "output line") {
			n++
			if !strings.HasPrefix(l, outputPad) {
				t.Errorf("output line is not padded: %q", l)
			}
		}
		if w := lipgloss.Width(l); w > m.w {
			t.Errorf("line is %d cells wide, want <= %d: %q", w, m.w, l)
		}
	}
	if n == 0 {
		t.Fatal("the output never reached the body")
	}
}

// The status bar is one line and never wider than the terminal.
func TestStatusBarIsOneLine(t *testing.T) {
	m := actionModel(t, 60, 24)
	line := m.statusLine()
	if strings.Contains(line, "\n") {
		t.Fatalf("status bar spans lines: %q", line)
	}
	if w := lipgloss.Width(line); w > m.w {
		t.Fatalf("status bar is %d cells wide, want <= %d", w, m.w)
	}
}

// The table is one column: language → default.
func TestTableIsLanguageArrowDefault(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.Rows = []LangRow{{Language: "python", Default: "python3"}}
	body := m.body()
	if len(body) < 1 || !strings.Contains(body[0], "python") || !strings.Contains(body[0], "→ python3") {
		t.Fatalf("row is not language → default: %v", body)
	}
}
