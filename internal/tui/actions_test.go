package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// actionModel is a dashboard with a deterministic environment: an empty env, so
// nothing is discovered from the host and every test sees the same thing.
func actionModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel(rows(), config.Default(), Options{Env: map[string]string{}, OS: "linux"})
	m.w, m.h = w, h
	return m
}

func press(t *testing.T, m Model, kt tea.KeyType) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: kt})
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return mm, cmd
}

// ---- `r`: where does the file come from? ----------------------------------

// r resolves its file in a fixed order. Without this the key was dead: it was
// gated on Options.File, which nothing ever set.
func TestRunPrefersTheExplicitFile(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.File = "/tmp/explicit.py"
	m.opts.Env = map[string]string{"ZED_FILE": "/tmp/from-zed.py"}
	file, ok := m.runFile()
	if !ok || file != "/tmp/explicit.py" {
		t.Fatalf("runFile() = %q, %v; want the explicit path", file, ok)
	}
}

func TestRunFallsBackToZedFile(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.Env = map[string]string{"ZED_FILE": "/tmp/from-zed.py"}
	file, ok := m.runFile()
	if !ok || file != "/tmp/from-zed.py" {
		t.Fatalf("runFile() = %q, %v; want ZED_FILE", file, ok)
	}
}

func TestRunIgnoresBlankZedFile(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.Env = map[string]string{"ZED_FILE": "   "}
	if _, ok := m.runFile(); ok {
		t.Fatal("a blank ZED_FILE was accepted as a path")
	}
}

// With no file source at all, r must ask rather than print a dead end.
func TestRunAsksForAPathWhenThereIsNoSource(t *testing.T) {
	m := actionModel(t, 80, 24)
	mm, cmd := send(t, m, "r")
	if cmd != nil {
		t.Fatalf("r ran something with no path: %T", cmd)
	}
	if mm.prompt == nil {
		t.Fatal("no file source and no prompt: r is still a dead end")
	}
	if mm.LastAction != "run" {
		t.Errorf("LastAction = %q, want run", mm.LastAction)
	}
}

func TestRunWithASourceDoesNotPrompt(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.File = "/tmp/a.py"
	mm, cmd := send(t, m, "r")
	if mm.prompt != nil {
		t.Fatal("prompted even though a file was known")
	}
	if cmd == nil {
		t.Fatal("r did not hand the terminal to the run")
	}
}

// ---- the path prompt ------------------------------------------------------

func openPrompt(t *testing.T) Model {
	t.Helper()
	m := actionModel(t, 80, 24)
	m, _ = send(t, m, "r")
	if m.prompt == nil {
		t.Fatal("prompt did not open")
	}
	return m
}

func TestPromptTypesAndBackspaces(t *testing.T) {
	m := openPrompt(t)
	for _, ch := range []string{"/", "t", "m", "p", "/", "a", ".", "p", "y"} {
		m, _ = send(t, m, ch)
	}
	if got := string(m.prompt.value); got != "/tmp/a.py" {
		t.Fatalf("typed %q, want /tmp/a.py", got)
	}
	m, _ = press(t, m, tea.KeyBackspace)
	if got := string(m.prompt.value); got != "/tmp/a.p" {
		t.Fatalf("after backspace %q, want /tmp/a.p", got)
	}
	// backspace on an empty prompt must not panic or wrap around
	m.prompt.value = nil
	m, _ = press(t, m, tea.KeyBackspace)
	if len(m.prompt.value) != 0 {
		t.Fatalf("backspace on empty left %q", string(m.prompt.value))
	}
}

// A path can be non-ASCII; deleting must remove a rune, not a byte.
func TestPromptHandlesMultiByteRunes(t *testing.T) {
	m := openPrompt(t)
	for _, ch := range []string{"π", ".", "p", "y"} {
		m, _ = send(t, m, ch)
	}
	if got := string(m.prompt.value); got != "π.py" {
		t.Fatalf("typed %q, want π.py", got)
	}
	m, _ = press(t, m, tea.KeyBackspace)
	if got := string(m.prompt.value); got != "π.p" {
		t.Fatalf("after backspace %q, want π.p", got)
	}
}

// While the prompt is open, keys belong to the prompt: q must not quit.
func TestPromptSwallowsDashboardKeys(t *testing.T) {
	m := openPrompt(t)
	m, _ = send(t, m, "q")
	if m.Quitting {
		t.Fatal("q quit the dashboard instead of being typed")
	}
	if got := string(m.prompt.value); got != "q" {
		t.Fatalf("value %q, want q", got)
	}
}

func TestPromptEscCancelsWithoutRunning(t *testing.T) {
	m := openPrompt(t)
	m, _ = send(t, m, "/tmp/a.py")
	mm, cmd := press(t, m, tea.KeyEsc)
	if mm.prompt != nil {
		t.Fatal("esc left the prompt open")
	}
	if cmd != nil {
		t.Fatalf("esc started a run: %T", cmd)
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "cancelled") {
		t.Errorf("no cancellation reported: %v", mm.Output)
	}
}

func TestPromptEnterRunsTheTypedPath(t *testing.T) {
	m := openPrompt(t)
	for _, ch := range []string{"/", "t", "m", "p", "/", "a", ".", "p", "y"} {
		m, _ = send(t, m, ch)
	}
	mm, cmd := press(t, m, tea.KeyEnter)
	if mm.prompt != nil {
		t.Fatal("enter left the prompt open")
	}
	if cmd == nil {
		t.Fatal("enter did not start the run")
	}
	if mm.LastAction != "run" {
		t.Errorf("LastAction = %q, want run", mm.LastAction)
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "/tmp/a.py") {
		t.Errorf("the path was not reported: %v", mm.Output)
	}
}

func TestPromptEnterOnEmptyPathDoesNotRun(t *testing.T) {
	m := openPrompt(t)
	mm, cmd := press(t, m, tea.KeyEnter)
	if cmd != nil {
		t.Fatalf("an empty path started a run: %T", cmd)
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "no path") {
		t.Errorf("no explanation for the empty path: %v", mm.Output)
	}
}

func TestPromptViewIsWindowed(t *testing.T) {
	m := openPrompt(t)
	m, _ = send(t, m, "/tmp/a.py")
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.h {
		t.Fatalf("prompt view emits %d lines, want %d", len(lines), m.h)
	}
	if !strings.Contains(lines[len(lines)-1], "esc cancel") {
		t.Fatalf("footer not on the last row: %q", lines[len(lines)-1])
	}
	if !strings.Contains(strings.Join(lines, "\n"), "/tmp/a.py") {
		t.Error("what was typed is not on screen")
	}
}

// ---- mouse: off by default, `m` toggles -----------------------------------

// Mouse capture makes the terminal stop sending selection to the app, so a
// dashboard you copy output out of must not enable it unless asked.
func TestMouseIsOffByDefault(t *testing.T) {
	m := actionModel(t, 80, 24)
	if m.Mouse {
		t.Fatal("mouse capture is on by default; text selection would be blocked")
	}
}

func TestMouseToggleTellsTheTerminal(t *testing.T) {
	m := actionModel(t, 80, 24)
	on, cmd := send(t, m, "m")
	if !on.Mouse {
		t.Fatal("m did not turn mouse capture on")
	}
	if cmd == nil {
		t.Fatal("m returned no command; the terminal was never told")
	}
	if got, want := fmt.Sprintf("%T", cmd()), fmt.Sprintf("%T", tea.EnableMouseCellMotion()); got != want {
		t.Errorf("m on produced %s, want %s", got, want)
	}
	off, cmd := send(t, on, "m")
	if off.Mouse {
		t.Fatal("m did not turn mouse capture off")
	}
	if cmd == nil {
		t.Fatal("m returned no command on the way off")
	}
	if got, want := fmt.Sprintf("%T", cmd()), fmt.Sprintf("%T", tea.DisableMouse()); got != want {
		t.Errorf("m off produced %s, want %s", got, want)
	}
}

func TestFooterAdvertisesTheMouseToggle(t *testing.T) {
	m := actionModel(t, 120, 24)
	last := lastLine(m.View())
	if !strings.Contains(last, "m mouse") {
		t.Fatalf("footer does not mention the mouse toggle: %q", last)
	}
	if !strings.Contains(last, "q quit") {
		t.Fatalf("footer lost q quit: %q", last)
	}
}

// ---- `u`: the check must not run inside Update ----------------------------

// A synchronous fetch freezes the whole dashboard until every product answers.
func TestUpdatesDoesNotBlockUpdate(t *testing.T) {
	slow := func(string) ([]byte, error) {
		time.Sleep(250 * time.Millisecond)
		return nil, errors.New("offline")
	}
	m := actionModel(t, 80, 24)
	m.opts.Fetcher = sources.NewFetcher(t.TempDir()+"/sources.json", time.Minute, slow)

	start := time.Now()
	mm, cmd := send(t, m, "u")
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Update took %v with a slow fetcher: the network call is still synchronous", elapsed)
	}
	if cmd == nil {
		t.Fatal("u returned no command, so the check cannot run in the background")
	}
	if !mm.updatesRunning {
		t.Fatal("no in-flight flag set; a second u would start a duplicate check")
	}
	if got := strings.Join(mm.Output, "\n"); !strings.Contains(got, "checking") {
		t.Errorf("no progress line while the check runs: %q", got)
	}
	if got := strings.Join(mm.Output, "\n"); strings.Contains(got, "no toolchains installed") {
		t.Errorf("results appeared inside Update, so it is not asynchronous: %q", got)
	}

	msg := cmd()
	um, ok := msg.(updatesMsg)
	if !ok {
		t.Fatalf("the command produced %T, want updatesMsg", msg)
	}
	if len(um.lines) == 0 {
		t.Fatal("updatesMsg carried no lines")
	}

	done, _ := mm.Update(um)
	dm := done.(Model)
	if dm.updatesRunning {
		t.Error("the in-flight flag was not cleared when the check finished")
	}
	if got := strings.Join(dm.Output, "\n"); !strings.Contains(got, um.lines[len(um.lines)-1]) {
		t.Errorf("the results never reached the output pane:\n%s", got)
	}
}

func TestUpdatesRefusesToStartASecondCheck(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.opts.Fetcher = sources.NewFetcher(t.TempDir()+"/sources.json", time.Minute,
		func(string) ([]byte, error) { return nil, errors.New("offline") })

	m, _ = send(t, m, "u")
	mm, cmd := send(t, m, "u")
	if cmd != nil {
		t.Fatalf("a second u started another check: %T", cmd)
	}
	if !strings.Contains(strings.Join(mm.Output, "\n"), "already") {
		t.Errorf("no explanation for the ignored press: %v", mm.Output)
	}
}

// Even with no fetcher the key must say so, not silently do nothing.
func TestUpdatesWithoutAFetcherExplainsItself(t *testing.T) {
	m := actionModel(t, 80, 24)
	mm, cmd := send(t, m, "u")
	if cmd == nil {
		t.Fatal("u returned no command")
	}
	done, _ := mm.Update(cmd())
	got := strings.Join(done.(Model).Output, "\n")
	if !strings.Contains(got, "version source") {
		t.Errorf("no explanation for the missing fetcher: %q", got)
	}
}

// ---- `d`: same treatment, because a cold cache costs 886ms ----------------

// Doctor re-lists directories on a cold cache. Measured on this machine: 886ms
// cold, 53ms warm, so it cannot run inside Update.
func TestDoctorDoesNotBlockUpdate(t *testing.T) {
	m := actionModel(t, 80, 24)
	mm, cmd := send(t, m, "d")
	if cmd == nil {
		t.Fatal("d returned no command: doctor still runs inside Update")
	}
	if got := strings.Join(mm.Output, "\n"); strings.Contains(got, "System") {
		t.Fatalf("the report appeared inside Update, so it is not asynchronous:\n%s", got)
	}
	if got := strings.Join(mm.Output, "\n"); !strings.Contains(got, "doctor") {
		t.Errorf("no progress line while doctor runs: %q", got)
	}
	done, _ := mm.Update(cmd())
	got := strings.Join(done.(Model).Output, "\n")
	if !strings.Contains(got, "System") {
		t.Errorf("the report never reached the pane:\n%s", got)
	}
}

// ---- output is wrapped, not truncated -------------------------------------

// The actionable end of a long line (a flag, a path) is at the right, so
// truncating it away is what made doctor's advice unusable.
func TestLongOutputWrapsInsteadOfLosingItsTail(t *testing.T) {
	m := actionModel(t, 80, 24)
	m.append("warning: two installs share the tool id python; pin one with --path /usr/bin/python3.12")
	body := m.body()
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "/usr/bin/python3.12") {
		t.Fatalf("the end of a long line was dropped:\n%s", joined)
	}
	for i, l := range body {
		if w := ansi.StringWidth(l); w > m.w {
			t.Errorf("body line %d is %d cells wide, want <= %d: %q", i, w, m.w, l)
		}
	}
}

// The language table still truncates: it is a fixed two-column layout, and
// wrapping it would break the alignment.
func TestLanguageTableStillTruncates(t *testing.T) {
	m := actionModel(t, 20, 24)
	m.Rows = []LangRow{{Language: "some-very-long-language-name", Default: "a-very-long-toolchain-name"}}
	body := m.body()
	if len(body) != 1 {
		t.Fatalf("the table row wrapped into %d lines", len(body))
	}
	if w := ansi.StringWidth(body[0]); w > m.w {
		t.Errorf("table row is %d cells wide, want <= %d", w, m.w)
	}
}

func TestBodyLenMatchesBody(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Model)
	}{
		{"rows only", func(*Model) {}},
		{"scanning", func(m *Model) { m.Rows = nil }},
		{"short output", func(m *Model) { m.append("hi") }},
		{"multiline output", func(m *Model) { m.append("a\nb\nc") }},
		{"wrapping output", func(m *Model) { m.append(strings.Repeat("wrap me ", 40)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := actionModel(t, 40, 10)
			tc.apply(&m)
			if got, want := m.bodyLen(), len(m.body()); got != want {
				t.Fatalf("bodyLen() = %d but body() has %d lines; the scroll clamp would be wrong", got, want)
			}
		})
	}
}

// G must reach the true end of the wrapped body, and the clamp must not let
// Scroll run past it.
func TestScrollClampAccountsForWrappedLines(t *testing.T) {
	m := actionModel(t, 40, 10)
	for i := 0; i < 20; i++ {
		m.append("a fairly long output line that wraps at forty columns")
	}
	m, _ = send(t, m, "G")
	body := m.body()
	if m.Scroll+m.window() < len(body) {
		t.Fatalf("G left %d of %d body lines unreachable", len(body)-(m.Scroll+m.window()), len(body))
	}
	if max := m.bodyLen() - m.window(); m.Scroll > max {
		t.Fatalf("Scroll = %d, past the maximum %d", m.Scroll, max)
	}
	// and the rendered view must show the tail of the output
	view := m.View()
	if !strings.Contains(view, "forty columns") {
		t.Error("the last body line is not on screen after G")
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
