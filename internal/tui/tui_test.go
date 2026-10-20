package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zedrun/zedrun-go/internal/catalog"
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
	for _, want := range []string{"run", "doctor", "updates", "pick", "get", "quit"} {
		if !strings.Contains(strings.ToLower(v), want) {
			t.Errorf("View footer missing %q:\n%s", want, v)
		}
	}
}

func TestActionOutputAccumulates(t *testing.T) {
	// isolatedOpts: this is about accumulation and visibility, not about the
	// host's toolchains. Probing the real filesystem made it take 14s.
	m := NewModel(rows(), config.Default(), isolatedOpts(t))
	mm, _ := send(t, m, "u")
	if len(mm.Output) == 0 {
		t.Fatal("an action produced no output")
	}
	mm2, _ := send(t, mm, "d")
	if len(mm2.Output) <= len(mm.Output) {
		t.Fatal("output did not accumulate across actions")
	}
	// Output wraps instead of truncating now, so the newest line can be split
	// across rows. Check its last chunk, which is the part truncation used to
	// eat — asserting on the whole truncated line no longer means anything.
	chunks := mm2.wrap(mm2.Output[len(mm2.Output)-1])
	want := strings.TrimSpace(chunks[len(chunks)-1])
	if want == "" && len(chunks) > 1 {
		want = strings.TrimSpace(chunks[len(chunks)-2])
	}
	if !strings.Contains(mm2.View(), want) {
		t.Fatalf("the newest output line is not shown in the view (want %q)", want)
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

// isolatedOpts keeps the background scan off the real filesystem.
func isolatedOpts(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	return Options{
		Env: map[string]string{
			"HOME": home, "PATH": filepath.Join(home, "empty"),
			"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
			"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
			"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		},
		OS: "linux",
	}
}

func TestInitScansInTheBackground(t *testing.T) {
	m := NewModel(nil, config.Default(), isolatedOpts(t))
	if !strings.Contains(m.View(), "scanning") {
		t.Fatalf("the view does not say it is scanning before the rows arrive:\n%s", m.View())
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command, so the table would never load")
	}
	updated, _ := m.Update(cmd())
	mm := updated.(Model)
	if len(mm.Rows) != len(catalog.Languages) {
		t.Fatalf("after the scan the table has %d rows, want %d", len(mm.Rows), len(catalog.Languages))
	}
	if strings.Contains(mm.View(), "scanning") {
		t.Fatal("the view still says it is scanning after the rows arrived")
	}
}

func TestInitIsNoOpWhenRowsAreSupplied(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init returned a command even though rows were already supplied")
	}
}

func TestRowsMsgFillsTheTable(t *testing.T) {
	m := NewModel(nil, config.Default(), Options{})
	updated, _ := m.Update(rowsMsg([]LangRow{{Language: "c", Default: "gcc"}}))
	mm := updated.(Model)
	if len(mm.Rows) != 1 || mm.Rows[0].Language != "c" {
		t.Fatalf("Rows = %+v", mm.Rows)
	}
}

func TestCursorResetsWhenRowsShrink(t *testing.T) {
	m := NewModel(rows(), config.Default(), Options{})
	m, _ = send(t, m, "down")
	m, _ = send(t, m, "down")
	updated, _ := m.Update(rowsMsg([]LangRow{{Language: "c", Default: "gcc"}}))
	if got := updated.(Model).Cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0 after the table shrank under it", got)
	}
}

func TestKeysAreSafeBeforeTheScanFinishes(t *testing.T) {
	m := NewModel(nil, config.Default(), Options{})
	for _, k := range []string{"down", "up", "t", "p"} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if _, ok := updated.(Model); !ok {
			t.Fatalf("key %q broke the model before the scan finished", k)
		}
	}
}
