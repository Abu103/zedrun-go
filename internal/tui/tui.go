// Package tui is the interactive dashboard. It drives the same core the CLI
// does, and every action's output lands in a scrollable pane rather than taking
// over the terminal. The model is pure: tests drive Update with synthetic
// messages and never need a real terminal.
package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/doctor"
	"github.com/zedrun/zedrun-go/internal/installer"
	"github.com/zedrun/zedrun-go/internal/planner"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/provision"
	"github.com/zedrun/zedrun-go/internal/sources"
	"github.com/zedrun/zedrun-go/internal/versions"
)

// LangRow is one line of the language table.
type LangRow struct {
	Language string
	Default  string
}

// Options carries everything the TUI needs from its caller.
type Options struct {
	Env     map[string]string
	OS      string
	Cache   *detect.ProbeCache
	Fetcher *sources.Fetcher
	File    string
}

// Model is the bubbletea model.
type Model struct {
	Rows       []LangRow
	Cursor     int
	Pre        bool
	Quitting   bool
	LastAction string
	Output     []string

	Scroll int // first visible body line

	// Mouse is mouse capture. It is off by default because capture stops the
	// terminal from selecting text, and the point of this dashboard is reading
	// output you may want to copy. `m` toggles it.
	Mouse bool

	confirmInstall bool // set after an `i` preview; a second `i` applies
	updatesRunning bool // set while an update check is in flight

	// picker is non-nil while the tool picker overlay is open.
	picker *picker

	// prompt is non-nil while the `r` path prompt is open.
	prompt *pathPrompt

	// getMenu is non-nil while the `g` install overlay is open.
	getMenu *getMenu

	// updMenu is non-nil while the `U` update overlay is open.
	updMenu *updMenu

	// upd collects the outdated tools during an update check, so the U overlay
	// does not have to re-run the whole network pass to offer them.
	upd []updChoice

	// wantUpd is set by the U key: the check it triggers should open the update
	// overlay when it finishes, rather than leaving the user to press U again.
	wantUpd bool

	// checked and nOutdated feed the status bar. They are set when an update
	// check lands, never recomputed in View: the check is a network pass.
	checked   bool
	nOutdated int

	cfg  config.Config
	opts Options
	w, h int
}

// maxOutput caps the output pane; older lines are dropped first.
const maxOutput = 2000

// headerRows is the fixed header: title, subtitle, status bar.
const headerRows = 3

// picker lists the installs of one language and lets the user pin one.
type picker struct {
	lang    string
	choices []choice
	cursor  int
}

type choice struct {
	Tool, Version, Path string
	OK                  bool
}

// pathPrompt is a minimal line editor for the `r` file path: runes, backspace,
// enter and esc. Hand-rolled on purpose — a one-field prompt is not worth a
// dependency, and the dashboard is otherwise free of them.
type pathPrompt struct {
	label string
	value []rune
}

// getChoice is one installable route offered in the overlay.
type getChoice struct {
	lang   string // language that is missing a toolchain
	tool   string // catalog tool id the route installs
	method provision.Method
}

// getMenu is the install overlay: every route that would give a language a
// toolchain, best first, ready to run.
type getMenu struct {
	choices []getChoice
	cursor  int
}

// updChoice is one outdated tool and the command that would update it.
type updChoice struct {
	tool       string
	installed  string
	latest     string // upstream's latest
	achievable string // what the command will actually install; "" when unknown
	method     provision.Method
}

// installTarget is the version this choice will actually install. A manager that
// answered for itself wins over upstream's number, because that is the one the
// command will land.
func (c updChoice) installTarget() string {
	if c.achievable != "" {
		return c.achievable
	}
	return c.latest
}

// updMenu is the update overlay: what is outdated, and how to fix it.
type updMenu struct {
	choices []updChoice
	cursor  int
}

// handle applies one key and reports whether the prompt was submitted or
// cancelled. Anything else is editing.
func (p *pathPrompt) handle(msg tea.KeyMsg) (submit, cancel bool) {
	switch msg.String() {
	case "enter":
		return true, false
	case "esc":
		return false, true
	case "backspace", "ctrl+h":
		if n := len(p.value); n > 0 {
			p.value = p.value[:n-1]
		}
	case "ctrl+u":
		p.value = nil
	default:
		// Runes only: while the prompt is open, `q` is a character, not a quit.
		if msg.Type == tea.KeyRunes {
			p.value = append(p.value, msg.Runes...)
		}
	}
	return false, false
}

// text is what has been typed so far.
func (p *pathPrompt) text() string { return string(p.value) }

// NewModel builds the dashboard model.
func NewModel(rows []LangRow, cfg config.Config, opts Options) Model {
	return Model{Rows: rows, cfg: cfg, opts: opts, w: 80, h: 24}
}

// Width and Height report the last window size seen.
func (m Model) Width() int  { return m.w }
func (m Model) Height() int { return m.h }

// rowsMsg carries a finished toolchain scan into the model.
type rowsMsg []LangRow

// runDoneMsg reports a finished `r` run.
type runDoneMsg struct{ err error }

// updatesMsg carries a finished update check into the model. It carries the
// outdated tools too: the command that fills it runs against a copy of the
// model, so anything that copy collected has to travel in the message.
type updatesMsg struct {
	lines    []string
	upd      []updChoice
	outdated int // every outdated tool, including ones with no update route
}

// doctorMsg carries a finished diagnostics run into the model.
type doctorMsg struct{ text string }

// Init kicks off the toolchain scan in the background, so the dashboard draws
// immediately instead of blocking for seconds while every language is resolved.
func (m Model) Init() tea.Cmd {
	if m.Rows != nil {
		return nil
	}
	cfg, opts := m.cfg, m.opts
	return func() tea.Msg { return rowsMsg(Rows(cfg, opts)) }
}

// Update handles keys, mouse and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case rowsMsg:
		m.Rows = []LangRow(msg)
		if m.Cursor >= len(m.Rows) {
			m.Cursor = 0
		}
		m.clamp()
		return m, nil
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clamp()
		return m, nil
	case runDoneMsg:
		if msg.err != nil {
			m.append("run failed: " + msg.err.Error())
		} else {
			m.append("run: finished")
		}
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case updatesMsg:
		m.updatesRunning = false
		m.appendLines(msg.lines)
		m.upd = msg.upd
		m.nOutdated = msg.outdated
		// Without a version source nothing was compared, so "0 outdated"
		// would be a claim nobody checked.
		m.checked = m.opts.Fetcher != nil
		m.Scroll = m.bodyLen()
		m.clamp()
		if m.wantUpd {
			m.wantUpd = false
			m.openUpdMenu()
		}
		return m, nil
	case updDoneMsg:
		// What was outdated has just changed; the old list would offer the
		// same update twice and the status bar would keep counting it.
		m.upd, m.nOutdated, m.checked = nil, 0, false
		m.append(msg.text)
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case getDoneMsg:
		m.append(msg.text)
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case doctorMsg:
		m.append(msg.text)
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case tea.MouseMsg:
		if m.picker != nil || m.prompt != nil || m.getMenu != nil || m.updMenu != nil {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.Scroll -= 3
		case tea.MouseButtonWheelDown:
			m.Scroll += 3
		}
		m.clamp()
		return m, nil
	case tea.KeyMsg:
		// Overlays get every key while they are open.
		if m.prompt != nil {
			return m.updatePrompt(msg)
		}
		if m.picker != nil {
			return m.updatePicker(msg)
		}
		if m.getMenu != nil {
			return m.updateGetMenu(msg)
		}
		if m.updMenu != nil {
			return m.updateUpdMenu(msg)
		}
		wasConfirm := m.confirmInstall
		m.confirmInstall = false
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.Quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
			m.followCursor()
		case "down", "j":
			if m.Cursor < len(m.Rows)-1 {
				m.Cursor++
			}
			m.followCursor()
		case "pgup", "ctrl+b":
			m.Scroll -= m.window()
		case "pgdown", "ctrl+f":
			m.Scroll += m.window()
		case "ctrl+u":
			m.Scroll -= m.window() / 2
		case "ctrl+d":
			m.Scroll += m.window() / 2
		case "home":
			m.Scroll = 0
		case "end", "G":
			m.Scroll = m.bodyLen()
		case "c":
			m.Output = nil
		case "m":
			m.Mouse = !m.Mouse
			if m.Mouse {
				m.append("mouse: on — the wheel scrolls, but the terminal no longer selects text")
			} else {
				m.append("mouse: off — the terminal selects text again")
			}
			m.Scroll = m.bodyLen()
			m.clamp()
			// Capture is a terminal mode, so the program has to be told.
			if m.Mouse {
				return m, tea.EnableMouseCellMotion
			}
			return m, tea.DisableMouse
		case "p":
			m.Pre = !m.Pre
			m.append("channel: " + m.channelLabel())
		case "r":
			m.LastAction = "run"
			file, ok := m.runFile()
			if !ok {
				// Nothing knows which file to run, so ask rather than print a
				// dead end — this is what made `r` look broken.
				m.prompt = &pathPrompt{label: "run which file?"}
				m.clamp()
				return m, nil
			}
			m.append("run: " + file)
			m.Scroll = m.bodyLen()
			m.clamp()
			return m, m.execRun(file)
		case "d":
			m.LastAction = "doctor"
			m.append("running doctor…")
			m.Scroll = m.bodyLen()
			m.clamp()
			// Doctor is slow on a cold cache — 886ms here, nearly all of it
			// listing /mnt PATH entries — so it does not run inside Update.
			return m, func() tea.Msg { return doctorMsg{m.doctorText()} }
		case "u":
			m.LastAction = "updates"
			if m.updatesRunning {
				m.append("updates: already checking")
				m.Scroll = m.bodyLen()
				m.clamp()
				return m, nil
			}
			m.updatesRunning = true
			m.upd = nil
			m.append("checking for updates…")
			m.Scroll = m.bodyLen()
			m.clamp()
			// Every product's version source is a network round trip. Running
			// that inside Update freezes the dashboard until the slowest one
			// answers, so it goes out as a command and reports back.
			return m, m.checkCmd()
		case "U":
			m.LastAction = "update"
			// The overlay is only as good as the list behind it, and that list
			// is filled by a network pass. Run it, then open the overlay when
			// it lands — pressing U on a cold dashboard would otherwise show
			// an empty menu.
			if m.updatesRunning {
				m.append("update: already checking")
				m.Scroll = m.bodyLen()
				m.clamp()
				return m, nil
			}
			if m.opts.Fetcher == nil {
				m.append("update: no version source configured")
				m.Scroll = m.bodyLen()
				m.clamp()
				return m, nil
			}
			m.updatesRunning = true
			m.wantUpd = true
			m.upd = nil
			m.append("checking for updates…")
			m.Scroll = m.bodyLen()
			m.clamp()
			return m, m.checkCmd()
		case "t":
			m.act("pick")
			m.Scroll = m.bodyLen()
		case "g":
			m.LastAction = "get"
			m.openGetMenu()
		case "i":
			m.LastAction = "install"
			if wasConfirm {
				m.install(false)
			} else {
				m.install(true)
				m.confirmInstall = true
			}
			m.Scroll = m.bodyLen()
		}
		m.clamp()
	}
	return m, nil
}

// ---- viewport -------------------------------------------------------------

// window is how many body rows fit: height minus header and the footer row.
func (m Model) window() int {
	w := m.h - headerRows - 1
	if w < 1 {
		return 1
	}
	return w
}

// body returns every scrollable line (language rows, then output pane).
func (m Model) body() []string {
	sel := lipgloss.NewStyle().Reverse(true)
	dim := lipgloss.NewStyle().Faint(true)
	var lines []string
	if m.Rows == nil {
		lines = append(lines, dim.Render("  scanning toolchains…"))
	}
	for i, r := range m.Rows {
		line := fmt.Sprintf("  %-12s %s", r.Language, r.Default)
		line = m.fit(line)
		if i == m.Cursor {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}
	if len(m.Output) > 0 {
		lines = append(lines, "")
		for _, l := range m.Output {
			lines = append(lines, m.wrap(l)...)
		}
	}
	return lines
}

// bodyLen is the length of the rendered body. It deliberately renders rather
// than re-deriving the count: wrapping depends on the terminal width, and two
// copies of that arithmetic drift apart, which silently breaks the scroll clamp.
func (m Model) bodyLen() int { return len(m.body()) }

// clamp keeps Scroll inside [0, bodyLen-window].
func (m *Model) clamp() {
	max := m.bodyLen() - m.window()
	if max < 0 {
		max = 0
	}
	if m.Scroll > max {
		m.Scroll = max
	}
	if m.Scroll < 0 {
		m.Scroll = 0
	}
}

// followCursor scrolls so the language cursor stays inside the window.
func (m *Model) followCursor() {
	if m.Cursor < m.Scroll {
		m.Scroll = m.Cursor
	} else if m.Cursor >= m.Scroll+m.window() {
		m.Scroll = m.Cursor - m.window() + 1
	}
}

// fit truncates a line to the terminal width so it never wraps. Used for the
// table and the chrome, where a fixed layout matters more than the tail.
func (m Model) fit(s string) string {
	if m.w <= 0 {
		return s
	}
	return ansi.Truncate(s, m.w, "…")
}

// wrap breaks an output line to the terminal width. Output wraps rather than
// truncates because the actionable end of a long line — a flag, a path — is at
// the right, and cutting it off is what made doctor's advice unusable.
func (m Model) wrap(s string) []string {
	if m.w <= 0 {
		return []string{s}
	}
	return strings.Split(ansi.Wrap(s, m.w, ""), "\n")
}

// View renders the dashboard, windowed to the terminal: header, a slice of the
// body, and a footer that is always on the last row.
func (m Model) View() string {
	if m.prompt != nil {
		return m.promptView()
	}
	if m.picker != nil {
		return m.pickerView()
	}
	if m.getMenu != nil {
		return m.getViewMenu()
	}
	if m.updMenu != nil {
		return m.getViewUpdMenu()
	}
	body := m.body()
	win := m.window()
	start := m.Scroll
	if start > len(body) {
		start = len(body)
	}
	end := start + win
	if end > len(body) {
		end = len(body)
	}
	sub := fmt.Sprintf("%s · %d languages · %s channel (p)", m.osName(), len(m.Rows), m.channelLabel())
	if len(body) > win {
		sub += fmt.Sprintf(" · lines %d-%d/%d · pgup/pgdn scroll", start+1, end, len(body))
	}
	return m.frame(sub, body[start:end], footerFor(m.w))
}

// frame is the fixed chrome every dashboard screen shares: the title, a
// subtitle, the status bar, then the body, then the key hint pinned to the
// last row. The body is clipped to the window, so the result is always exactly
// m.h lines and the header and footer stay put while the body scrolls.
func (m Model) frame(sub string, visible []string, hint string) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dim := lipgloss.NewStyle().Faint(true)
	win := m.window()
	if len(visible) > win {
		visible = visible[:win]
	}
	var b strings.Builder
	b.WriteString(m.fit(title.Render("zedrun-go — Code Runner for Zed")))
	b.WriteString("\n")
	b.WriteString(m.fit(dim.Render(sub)))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	for _, l := range visible {
		b.WriteString(l)
		b.WriteString("\n")
	}
	// pad so the footer is pinned to the last row
	for i := len(visible); i < win; i++ {
		b.WriteString("\n")
	}
	b.WriteString(m.fit(dim.Render(hint)))
	return b.String()
}

// statusLine is the one-line bar under the subtitle: the toolchain the cursor
// is on, and how many tools are outdated. It reads only state the model already
// holds, so rendering it never touches the network or the disk.
func (m Model) statusLine() string {
	dim := lipgloss.NewStyle().Faint(true)
	warn := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	var parts []string
	if len(m.Rows) == 0 {
		parts = append(parts, dim.Render("no toolchain info yet"))
	} else if row := m.selected(); row.Default == "-" {
		parts = append(parts, warn.Render(row.Language+": no toolchain")+dim.Render(" (g installs one)"))
	} else {
		parts = append(parts, row.Language+" → "+row.Default)
	}
	switch {
	case m.updatesRunning:
		parts = append(parts, dim.Render("checking updates…"))
	case !m.checked:
		parts = append(parts, dim.Render("updates not checked (u)"))
	case m.nOutdated == 0:
		parts = append(parts, good.Render("all up to date"))
	case len(m.upd) > 0:
		parts = append(parts, warn.Render(fmt.Sprintf("%d outdated", m.nOutdated))+dim.Render(" (U updates)"))
	default:
		parts = append(parts, warn.Render(fmt.Sprintf("%d outdated", m.nOutdated))+dim.Render(" (no update route)"))
	}
	return m.fit(strings.Join(parts, dim.Render(" · ")))
}

// footers are the key hint, widest first. Each is a complete line, not a
// truncation of the one above: a hint cut mid-word advertises a key the user
// cannot read, so the dashboard picks the longest one that fits instead.
var footers = []string{
	"r run · d doctor · u updates · U update · t pick · g get · i zed setup · c clear · m mouse · q quit",
	"r run · d doctor · u updates · U update · t pick · g get · i zed · m mouse · q quit",
	"r run · d doctor · u updates · U update · t pick · g get · i zed · q quit",
}

// footerFor picks the widest hint that fits w columns.
func footerFor(w int) string {
	for _, f := range footers {
		if w <= 0 || lipgloss.Width(f) <= w {
			return f
		}
	}
	return footers[len(footers)-1]
}

// Run starts the dashboard. It returns the exit code to use. The toolchain scan
// runs inside the program, so the first frame is on screen immediately.
//
// Mouse capture is deliberately not enabled at startup: it stops the terminal
// from selecting text, and reading output you want to copy is the whole point.
// `m` turns it on for wheel scrolling.
func Run(cfg config.Config, opts Options) (int, error) {
	m := NewModel(nil, cfg, opts)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return 1, err
	}
	return 0, nil
}

// Rows builds the language table by resolving each language's default toolchain.
func Rows(cfg config.Config, opts Options) []LangRow {
	keys := make([]string, 0, len(catalog.Languages))
	for k := range catalog.Languages {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := opts.Env
	if env == nil {
		env = environ()
	}
	osName := opts.OS
	if osName == "" {
		osName = platform.OSName("")
	}
	// Resolving every language is the most expensive thing the tool does; always
	// run it against a cache so the directory listings are shared across tools.
	cache := opts.Cache
	if cache == nil {
		cache = detect.NewProbeCache("")
	}

	rows := make([]LangRow, 0, len(keys))
	for _, k := range keys {
		def := "-"
		if res, err := planner.Resolve(k, "", cfg, cache, "", env, osName, nil); err == nil {
			def = res.Install.Tool
		}
		rows = append(rows, LangRow{Language: k, Default: def})
	}
	return rows
}

// runFile resolves the file `r` should run: an explicit path first, then
// ZED_FILE, which Zed exports when the dashboard runs as one of its tasks.
// It reports false when nothing knows, and the caller asks.
func (m Model) runFile() (string, bool) {
	if m.opts.File != "" {
		return m.opts.File, true
	}
	if f := strings.TrimSpace(m.env()["ZED_FILE"]); f != "" {
		return f, true
	}
	return "", false
}

// execRun hands the terminal to `zedrun-go run FILE` and takes it back after,
// so the program keeps its own stdin, stdout and colour.
func (m Model) execRun(file string) tea.Cmd {
	bin, err := os.Executable()
	if err != nil {
		bin = "zedrun-go"
	}
	return tea.ExecProcess(exec.Command(bin, "run", file),
		func(err error) tea.Msg { return runDoneMsg{err} })
}

// updatePrompt handles keys while the run-path prompt is open.
func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	submit, cancel := m.prompt.handle(msg)
	switch {
	case cancel:
		m.prompt = nil
		m.append("run: cancelled")
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case submit:
		file := strings.TrimSpace(m.prompt.text())
		m.prompt = nil
		if file == "" {
			m.append("run: no path given")
			m.Scroll = m.bodyLen()
			m.clamp()
			return m, nil
		}
		m.LastAction = "run"
		m.append("run: " + file)
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, m.execRun(file)
	}
	m.clamp()
	return m, nil
}

// promptView renders the path prompt: the label, what has been typed with a
// block cursor, and the key hint pinned to the last row.
func (m Model) promptView() string {
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Faint(true)
	// The input is the second line, not the third: on a short terminal the
	// clip would otherwise hide what you are typing.
	lines := []string{
		m.fit(bold.Render(m.prompt.label)),
		m.fit("  " + m.prompt.text() + "█"),
	}
	win := m.h - 1
	if win < 1 {
		win = 1
	}
	var b strings.Builder
	n := 0
	for _, l := range lines {
		if n >= win {
			break
		}
		b.WriteString(l + "\n")
		n++
	}
	for ; n < win; n++ {
		b.WriteString("\n")
	}
	b.WriteString(m.fit(dim.Render("type a path · enter run · esc cancel")))
	return b.String()
}

func (m *Model) act(name string) {
	m.LastAction = name
	switch name {
	case "run":
		file, ok := m.runFile()
		if !ok {
			m.append("run: no file to run — pass --file, set ZED_FILE, or press r and type a path")
			return
		}
		m.append("run: " + file)
	case "doctor":
		m.append(m.doctorText())
	case "updates":
		m.appendLines(m.updateLines())
	case "pick":
		row := m.selected()
		cs := m.choicesFor(row.Language)
		if len(cs) == 0 {
			m.append(fmt.Sprintf("pick: no installed toolchains found for %s", row.Language))
			return
		}
		m.picker = &picker{lang: row.Language, choices: cs}
	case "install":
		m.install(true)
	}
}

// install previews (dry) or applies the Zed tasks/keymap install.
func (m *Model) install(dry bool) {
	zed := platform.ZedConfigDir(m.env(), m.osName())
	bin, err := os.Executable()
	if err != nil {
		bin = "zedrun-go"
	}
	msgs, err := installer.Install(zed, bin, dry, nil)
	if err != nil {
		m.append("install failed: " + err.Error())
		return
	}
	for _, s := range msgs {
		m.append(s)
	}
	if dry {
		m.append("(preview — press i again to apply)")
	}
}

// choicesFor lists every install found for a language's runners.
func (m *Model) choicesFor(language string) []choice {
	lang, ok := catalog.Languages[language]
	if !ok {
		return nil
	}
	cache := m.opts.Cache
	if cache == nil {
		cache = detect.NewProbeCache("")
	}
	seen := map[string]bool{}
	var out []choice
	for _, r := range lang.Runners {
		for _, id := range r.Tool {
			if seen[id] {
				continue
			}
			seen[id] = true
			for _, in := range detect.FindInstalls(catalog.GetTool(id), m.cfg, cache, true, m.env(), m.osName()) {
				out = append(out, choice{Tool: in.Tool, Version: in.Version, Path: in.Path, OK: in.OK})
			}
		}
	}
	return out
}

// updatePicker handles keys while the picker overlay is open.
func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.picker = nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.choices)-1 {
			p.cursor++
		}
	case "enter":
		c := p.choices[p.cursor]
		m.picker = nil
		// Always pin by path: the tool id alone cannot tell two installs apart.
		out, err := config.SetPreference(config.UserConfigPath(m.env(), m.osName()), p.lang, c.Tool, c.Path)
		if err != nil {
			m.append("pick failed: " + err.Error())
			return m, nil
		}
		m.append("pick: " + out)
		wd, _ := os.Getwd()
		m.cfg, _ = config.Load(wd, m.env(), m.osName())
		cfg, opts := m.cfg, m.opts
		m.clamp()
		return m, func() tea.Msg { return rowsMsg(Rows(cfg, opts)) }
	}
	return m, nil
}

func (m Model) pickerView() string {
	p := m.picker
	sel := lipgloss.NewStyle().Reverse(true)
	dim := lipgloss.NewStyle().Faint(true)
	var lines []string
	lines = append(lines, m.fit(lipgloss.NewStyle().Bold(true).Render("pick toolchain for "+p.lang)), "")
	for i, c := range p.choices {
		ok := "ok"
		if !c.OK {
			ok = "broken"
		}
		line := m.fit(fmt.Sprintf("  %-10s %-10s %-7s %s", c.Tool, c.Version, ok, c.Path))
		if i == p.cursor {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}
	win := m.h - 1
	if win < 1 {
		win = 1
	}
	// keep the cursor row visible
	start := 0
	if cur := p.cursor + 2; cur >= win {
		start = cur - win + 1
	}
	end := start + win
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	n := 0
	for _, l := range lines[start:end] {
		b.WriteString(l + "\n")
		n++
	}
	for ; n < win; n++ {
		b.WriteString("\n")
	}
	b.WriteString(m.fit(dim.Render("↑/↓ select · enter pin · esc cancel")))
	return b.String()
}

// ---- install overlay ------------------------------------------------------

// openGetMenu builds the install overlay from the languages that have nothing
// usable, and opens it. A language that already resolves is skipped, so the
// list is exactly what the user would otherwise have to install by hand.
func (m *Model) openGetMenu() {
	choices := m.getChoices()
	if len(choices) == 0 {
		m.append("get: every language has a usable toolchain")
		m.Scroll = m.bodyLen()
		m.clamp()
		return
	}
	m.getMenu = &getMenu{choices: choices}
	m.clamp()
}

// getChoices lists one runnable route per language that has no toolchain, in
// catalog order. Only runnable routes are offered: an overlay whose enter key
// does nothing is worse than no overlay.
func (m Model) getChoices() []getChoice {
	cache := m.opts.Cache
	if cache == nil {
		cache = detect.NewProbeCache("")
	}
	env, osName := m.env(), m.osName()
	detected := provision.Detected(m.cfg, cache, env, osName)

	keys := make([]string, 0, len(catalog.Languages))
	for k := range catalog.Languages {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []getChoice
	for _, lang := range keys {
		if _, err := planner.Resolve(lang, "", m.cfg, cache, "", env, osName, nil); err == nil {
			continue
		}
		for _, r := range catalog.Languages[lang].Runners {
			for _, id := range r.Tool {
				ms := provision.Available(id, osName, detected, false)
				ms = provision.Order(ms, provision.OwnerFor(""))
				for _, method := range ms {
					if method.Runnable() {
						out = append(out, getChoice{lang: lang, tool: id, method: method})
						break
					}
				}
				break
			}
			break
		}
	}
	return out
}

// updateGetMenu handles keys while the install overlay is open. The overlay
// takes every key: a stray `q` closing the whole program from inside a menu is
// how a user loses their scrollback.
func (m Model) updateGetMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	g := m.getMenu
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.getMenu = nil
	case "up", "k":
		if g.cursor > 0 {
			g.cursor--
		}
	case "down", "j":
		if g.cursor < len(g.choices)-1 {
			g.cursor++
		}
	case "enter":
		if len(g.choices) == 0 {
			m.getMenu = nil
			return m, nil
		}
		c := g.choices[g.cursor]
		m.getMenu = nil
		m.append("get: " + c.lang + " via " + c.method.Manager)
		m.append("get: " + c.method.Command())
		m.Scroll = m.bodyLen()
		m.clamp()
		// The install goes out as a command: a package manager can take
		// minutes, and the dashboard must stay responsive while it runs.
		return m, m.installCmd(c)
	}
	return m, nil
}

// installCmd runs one route in the background and reports back as a message.
// Running it as a command rather than a bare goroutine is what makes the
// outcome testable: the test can call the command and feed the message back.
func (m Model) installCmd(c getChoice) tea.Cmd {
	env, osName := m.env(), m.osName()
	method := c.method
	tool := c.tool
	return func() tea.Msg {
		argv, ok, note := provision.Elevate(method, osName, os.Geteuid() == 0, env)
		if !ok {
			return getDoneMsg{"get: " + note}
		}
		method.Argv = argv
		// The installer's own output is the interesting half of the result:
		// a manager that says nothing is indistinguishable from one that
		// failed silently. It is captured rather than streamed, because the
		// overlay owns the screen while the install runs.
		var log cappedBuffer
		code := provision.Run(method, provision.RunOptions{
			Elevate: argv[0] == "sudo",
			Env:     env,
			Stdout:  &log,
			Stderr:  &log,
			Timeout: 10 * time.Minute,
		})
		if log.Len() > 0 {
			return getDoneMsg{"get:\n" + log.String()}
		}
		if code == 0 {
			return getDoneMsg{"get: " + tool + " done — `zedrun-go which " + tool + "` will confirm it"}
		}
		return getDoneMsg{fmt.Sprintf("get: %s exited %d", tool, code)}
	}
}

// getDoneMsg carries the outcome of an install back into the model.
type getDoneMsg struct{ text string }

// cappedBuffer is a bytes.Buffer that keeps only the last cap bytes. An
// installer's progress lines are not worth a scrollback pane; its final
// summary is.
type cappedBuffer struct {
	bytes.Buffer
	cap int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.cap <= 0 {
		b.cap = 4000
	}
	n, err := b.Buffer.Write(p)
	if b.Buffer.Len() > b.cap {
		// Drop whole lines from the front so the log does not start mid-word.
		s := b.Buffer.String()
		cut := b.Buffer.Len() - b.cap
		if i := strings.IndexByte(s[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		b.Buffer.Reset()
		b.Buffer.WriteString(s[cut:])
	}
	return n, err
}

// getViewMenu renders the overlay: one row per route, the cursor highlighted.
func (m Model) getViewMenu() string {
	g := m.getMenu
	sel := lipgloss.NewStyle().Reverse(true)
	dim := lipgloss.NewStyle().Faint(true)

	var lines []string
	lines = append(lines, m.fit(lipgloss.NewStyle().Bold(true).Render("get a toolchain")), "")
	// How much is actually missing, so the list has a headline rather than
	// looking like a menu of things the user already has.
	noun := "languages"
	if len(g.choices) == 1 {
		noun = "language"
	}
	lines = append(lines, m.fit(dim.Render(fmt.Sprintf("%d %s have nothing usable — enter installs the highlighted route", len(g.choices), noun))), "")
	for i, c := range g.choices {
		line := m.fit(fmt.Sprintf("  %-10s %-7s %s", c.lang, c.method.Manager, c.method.Command()))
		if i == g.cursor {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}

	win := m.h - 1
	if win < 1 {
		win = 1
	}
	start := 0
	if cur := g.cursor + 2; cur >= win {
		start = cur - win + 1
	}
	end := start + win
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	n := 0
	for _, l := range lines[start:end] {
		b.WriteString(l + "\n")
		n++
	}
	for ; n < win; n++ {
		b.WriteString("\n")
	}
	b.WriteString(m.fit(dim.Render("↑/↓ select · enter install · esc cancel")))
	return b.String()
}

// ---- update overlay -------------------------------------------------------

// openUpdMenu offers the outdated tools collected by the last update check.
// With nothing to update it says so: an empty menu reads as a bug.
func (m *Model) openUpdMenu() {
	if len(m.upd) == 0 {
		m.append("update: everything is up to date (run u to check)")
		m.Scroll = m.bodyLen()
		m.clamp()
		return
	}
	m.updMenu = &updMenu{choices: append([]updChoice(nil), m.upd...)}
	m.clamp()
}

// updateUpdMenu handles keys while the update overlay is open.
func (m Model) updateUpdMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	u := m.updMenu
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.updMenu = nil
	case "up", "k":
		if u.cursor > 0 {
			u.cursor--
		}
	case "down", "j":
		if u.cursor < len(u.choices)-1 {
			u.cursor++
		}
	case "enter":
		if len(u.choices) == 0 {
			m.updMenu = nil
			return m, nil
		}
		c := u.choices[u.cursor]
		m.updMenu = nil
		m.append("update: " + c.tool + " " + c.installed + " → " + c.installTarget())
		m.append("update: " + c.method.Command())
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, m.updateCmd(c)
	}
	return m, nil
}

// updateCmd runs one update in the background, like the install overlay.
func (m Model) updateCmd(c updChoice) tea.Cmd {
	env, osName := m.env(), m.osName()
	method := c.method
	tool := c.tool
	return func() tea.Msg {
		argv, ok, note := provision.Elevate(method, osName, os.Geteuid() == 0, env)
		if !ok {
			return updDoneMsg{"update: " + note}
		}
		method.Argv = argv
		var log cappedBuffer
		code := provision.Run(method, provision.RunOptions{
			Elevate: argv[0] == "sudo",
			Env:     env,
			Stdout:  &log,
			Stderr:  &log,
			Timeout: 10 * time.Minute,
		})
		if log.Len() > 0 {
			return updDoneMsg{"update:\n" + log.String()}
		}
		if code == 0 {
			return updDoneMsg{"update: " + tool + " done"}
		}
		return updDoneMsg{fmt.Sprintf("update: %s exited %d", tool, code)}
	}
}

// updDoneMsg carries the outcome of an update back into the model.
type updDoneMsg struct{ text string }

// getViewUpdMenu renders the update overlay inside the dashboard frame: the
// header, status bar and footer stay on screen and only the body is replaced.
// Each outdated tool is a block — name and versions, then the command — and
// both lines wrap rather than truncate, because the end of a command or the
// "upstream" note is the part the user needs.
func (m Model) getViewUpdMenu() string {
	u := m.updMenu
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Faint(true)

	head := []string{
		m.fit(bold.Render("update toolchains")),
		m.fit(dim.Render(fmt.Sprintf("%d outdated — enter runs the highlighted update", len(u.choices)))),
		"",
	}
	var rows []string
	starts := make([]int, len(u.choices))
	ends := make([]int, len(u.choices))
	for i, c := range u.choices {
		starts[i] = len(rows)
		rows = append(rows, m.updBlock(c, i == u.cursor)...)
		ends[i] = len(rows)
	}

	// The heading is pinned; the blocks scroll so the highlighted one is whole.
	room := m.window() - len(head)
	if room < 1 {
		room = 1
	}
	start := 0
	if u.cursor >= 0 && u.cursor < len(u.choices) {
		if ends[u.cursor] > room {
			start = ends[u.cursor] - room
		}
		if starts[u.cursor] < start {
			start = starts[u.cursor]
		}
	}
	end := start + room
	if end > len(rows) {
		end = len(rows)
	}
	visible := append(head, rows[start:end]...)
	sub := fmt.Sprintf("%s · update overlay · %s channel", m.osName(), m.channelLabel())
	return m.frame(sub, visible, "↑/↓ select · enter update · esc cancel")
}

// wrapTo wraps s to the terminal width minus indent, for callers that add the
// indent themselves. Wrapping the already-indented text would leave the indent
// to the wrapper, which is free to treat leading spaces as collapsible.
func (m Model) wrapTo(s string, indent int) []string {
	if m.w <= 0 {
		return []string{s}
	}
	width := m.w - indent
	if width < 10 {
		width = 10
	}
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

// indented puts first before the first line and rest before every other, then
// clips each to the terminal width as a last resort for very narrow windows.
func (m Model) indented(lines []string, first, rest string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = m.fit(first + l)
		} else {
			out[i] = m.fit(rest + l)
		}
	}
	return out
}

// updBlock renders one outdated tool as its wrapped lines. The highlighted
// block is drawn plain and reversed as a whole: colour escapes inside a
// reversed run would end the reverse part-way through the line.
func (m Model) updBlock(c updChoice, selected bool) []string {
	target := c.installTarget()
	lag := ""
	// The command lands the manager's version, which can trail upstream's. Say
	// so on the row, so the arrow never promises a number the command will not
	// install.
	if c.achievable != "" && !versions.SameVersion(c.achievable, c.latest) {
		lag = " (upstream " + c.latest + ")"
	}
	cmd := "$ " + c.method.Command()

	if selected {
		sel := lipgloss.NewStyle().Reverse(true)
		head := fmt.Sprintf("%-9s %s → %s%s", c.tool, c.installed, target, lag)
		lines := m.indented(m.wrapTo(head, 2), "▸ ", "  ")
		lines = append(lines, m.indented(m.wrapTo(cmd, 4), "    ", "    ")...)
		for i := range lines {
			lines[i] = sel.Render(lines[i])
		}
		return lines
	}

	warn := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	dim := lipgloss.NewStyle().Faint(true)
	head := warn.Render(fmt.Sprintf("%-9s", c.tool)) + " " + c.installed + " → " + good.Render(target) + dim.Render(lag)
	lines := m.indented(m.wrapTo(head, 2), "  ", "  ")
	for _, l := range m.indented(m.wrapTo(cmd, 4), "    ", "    ") {
		lines = append(lines, dim.Render(l))
	}
	return lines
}

// doctorText renders the diagnostics report. Slow enough on a cold cache that
// callers should run it in a command, not inside Update.
func (m *Model) doctorText() string {
	cache := m.opts.Cache
	if cache == nil {
		// The dashboard always passes a cache; tests and embedders may not.
		// Without one doctor re-lists every directory once per tool.
		cache = detect.NewProbeCache("")
	}
	report := doctor.Collect(m.cfg, cache, m.env(), m.osName(), nil)
	var b strings.Builder
	doctor.Render(report, &b, false)
	return strings.TrimRight(b.String(), "\n")
}

// checkCmd runs the update pass in the background and reports both the lines to
// show and the outdated tools worth offering an update for.
func (m *Model) checkCmd() tea.Cmd {
	fetcher := m.opts.Fetcher
	return func() tea.Msg {
		mm := *m
		lines, upd := mm.collectUpdates(fetcher)
		return updatesMsg{lines: lines, upd: upd, outdated: mm.nOutdated}
	}
}

// collectUpdates is updateLines plus the outdated-tool list, so the two are
// computed together and cannot disagree.
func (m *Model) collectUpdates(fetcher *sources.Fetcher) ([]string, []updChoice) {
	m.opts.Fetcher = fetcher
	lines := m.updateLines()
	return lines, m.upd
}

func (m *Model) updateLines() []string {
	if m.opts.Fetcher == nil {
		return []string{"updates: no version source configured"}
	}
	channel := "stable"
	if m.Pre {
		channel = "pre"
	}
	m.nOutdated = 0
	// Outdated rows stand out from the current ones; the update hint under one
	// is a different colour so the pair reads as a row and its remedy. Each is
	// styled as one run, so a substring search over the output still matches.
	outdatedRow := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	hintRow := lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	env, osName := m.env(), m.osName()
	cache := m.opts.Cache
	if cache == nil {
		cache = detect.NewProbeCache("")
	}

	ids := make([]string, 0, len(catalog.Tools))
	for id := range catalog.Tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	detected := provision.Detected(m.cfg, cache, env, osName)
	// One `winget list` call serves the whole pass: the index is memoised, so a
	// check touching ten winget packages still asks winget once.
	wingetIndex := provision.WingetIndexFunc(env, osName)

	// Driven by what is installed, not by which products happen to have a
	// version feed. Walking sources.Products skipped every tool without one, so
	// a machine with perl, sqlite3 and bash installed showed none of them and
	// the list was shorter than the machine's actual toolchain count. A tool
	// with no feed now says so instead of disappearing.
	var lines []string
	// Two separate sets, and they must stay separate: a tool id and a product
	// id are often the same string (bash, gcc, python), and sharing one map
	// made the product check drop the tool's only row.
	seenTool := map[string]bool{}
	seenProduct := map[string]bool{}
	for _, id := range ids {
		for _, inst := range detect.FindInstalls(catalog.GetTool(id), m.cfg, cache, true, env, osName) {
			if !inst.OK {
				continue
			}
			// One row per tool. Four bash installs are four installs of one
			// tool, and four identical rows is noise that hides the rest.
			if seenTool[inst.Tool] {
				continue
			}
			seenTool[inst.Tool] = true
			p, hasSource := sources.ProductForTool(inst.Tool)
			if !hasSource {
				lines = append(lines, fmt.Sprintf("%-8s %-12s %-12s %-11s %s",
					inst.Tool, inst.Version, "-", "no source", "nothing to compare against"))
				continue
			}
			// One line per product: clang and clang++ share a release feed.
			if seenProduct[p.ID] {
				continue
			}
			seenProduct[p.ID] = true
			state, target, bump := versions.Judge(inst.Version, sources.FetchLatest(p, m.opts.Fetcher), channel)
			row := fmt.Sprintf("%-8s %-12s %-12s %-11s %s", p.ID, inst.Version, target, state, bump)
			if state == "outdated" {
				m.nOutdated++
				row = outdatedRow.Render(row)
			}
			lines = append(lines, row)
			// "outdated" with no way to act on it is a complaint. When a manager
			// on this machine can do the upgrade, say exactly what it would run,
			// and keep it for the U overlay. When nothing can, say that too: a
			// silent row reads as a bug.
			if state == "outdated" {
				plan := provision.ResolveUpdate(inst.Tool, osName, inst.Version, target, inst.Origin, detected, env, wingetIndex())
				if plan.Method.Runnable() {
					m.upd = append(m.upd, updChoice{
						tool: inst.Tool, installed: inst.Version, latest: target,
						achievable: plan.Achievable, method: plan.Method,
					})
					hint := "update: " + plan.Method.Command()
					// The version the command will actually install. winget's own
					// answer can be older than upstream's — its Go manifest sits
					// at 1.27.0 while go.dev has 1.27.3 — and promising upstream's
					// number when the command lands the older one is the bug this
					// prevents. NextVersion refuses a version that is not newer,
					// or that belongs to a different product: Git's 2.55.0.5 is not
					// bash's next.
					if next := plan.NextVersion(inst.Version); next != "" {
						hint += " → " + next
						// "lags" is a claim about versions, not strings: winget
						// writes 7.6.7.0 where upstream writes 7.6.7, and those
						// are the same release.
						if !versions.SameVersion(next, target) {
							hint += " (" + plan.Method.Manager + " lags upstream " + target + ")"
						}
					}
					lines = append(lines, hintRow.Render(fmt.Sprintf("%-8s %s", "", hint)))
				} else if plan.Reason != "" {
					lines = append(lines, fmt.Sprintf("%-8s %s", "", plan.Reason))
				}
			}
		}
	}
	if len(lines) == 0 {
		return []string{"no toolchains installed to check"}
	}
	return lines
}

func (m *Model) selected() LangRow {
	if m.Cursor < 0 || m.Cursor >= len(m.Rows) {
		if len(m.Rows) == 0 {
			return LangRow{Language: "-", Default: "-"}
		}
		return m.Rows[0]
	}
	return m.Rows[m.Cursor]
}

func (m *Model) append(s string) {
	for _, line := range strings.Split(s, "\n") {
		m.Output = append(m.Output, line)
	}
	if over := len(m.Output) - maxOutput; over > 0 {
		m.Output = append([]string(nil), m.Output[over:]...)
	}
}

func (m *Model) appendLines(lines []string) {
	for _, l := range lines {
		m.append(l)
	}
}

func (m *Model) channelLabel() string {
	if m.Pre {
		return "pre"
	}
	return "stable"
}

func (m *Model) env() map[string]string {
	if m.opts.Env != nil {
		return m.opts.Env
	}
	return environ()
}

func (m *Model) osName() string {
	if m.opts.OS != "" {
		return m.opts.OS
	}
	return platform.OSName("")
}

func environ() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
