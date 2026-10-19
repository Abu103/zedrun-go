// Package tui is the interactive dashboard. It drives the same core the CLI
// does, and every action's output lands in a scrollable pane rather than taking
// over the terminal. The model is pure: tests drive Update with synthetic
// messages and never need a real terminal.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

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

	cfg  config.Config
	opts Options
	w, h int
}

// maxOutput caps the output pane; older lines are dropped first.
const maxOutput = 2000

// headerRows is the fixed header: title, subtitle, blank line.
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

// updatesMsg carries a finished update check into the model.
type updatesMsg struct{ lines []string }

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
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case doctorMsg:
		m.append(msg.text)
		m.Scroll = m.bodyLen()
		m.clamp()
		return m, nil
	case tea.MouseMsg:
		if m.picker != nil || m.prompt != nil {
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
		case "home", "g":
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
			m.append("checking for updates…")
			m.Scroll = m.bodyLen()
			m.clamp()
			// Every product's version source is a network round trip. Running
			// that inside Update freezes the dashboard until the slowest one
			// answers, so it goes out as a command and reports back.
			return m, func() tea.Msg { return updatesMsg{m.updateLines()} }
		case "t":
			m.act("pick")
			m.Scroll = m.bodyLen()
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
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dim := lipgloss.NewStyle().Faint(true)

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
	visible := body[start:end]

	var b strings.Builder
	b.WriteString(m.fit(title.Render("zedrun-go — Code Runner for Zed")))
	b.WriteString("\n")
	sub := fmt.Sprintf("%s · %d languages · %s channel (p)", m.osName(), len(m.Rows), m.channelLabel())
	if len(body) > win {
		sub += fmt.Sprintf(" · lines %d-%d/%d · pgup/pgdn scroll", start+1, end, len(body))
	}
	b.WriteString(m.fit(dim.Render(sub)))
	b.WriteString("\n\n")
	for _, l := range visible {
		b.WriteString(l)
		b.WriteString("\n")
	}
	// pad so the footer is pinned to the last row
	for i := len(visible); i < win; i++ {
		b.WriteString("\n")
	}
	b.WriteString(m.fit(dim.Render(footer)))
	return b.String()
}

// footer is the key hint. It fits 80 columns with room to spare, because a
// truncated hint is a hint for the keys nobody can find.
const footer = "r run · d doctor · u updates · t pick · i install · m mouse · c clear · q quit"

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

func (m *Model) updateLines() []string {
	if m.opts.Fetcher == nil {
		return []string{"updates: no version source configured"}
	}
	channel := "stable"
	if m.Pre {
		channel = "pre"
	}
	ids := make([]string, 0, len(sources.Products))
	for id := range sources.Products {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var lines []string
	for _, id := range ids {
		p := sources.Products[id]
		inst, ok := m.firstInstall(p)
		if !ok {
			continue
		}
		state, target, bump := versions.Judge(inst.Version, sources.FetchLatest(p, m.opts.Fetcher), channel)
		lines = append(lines, fmt.Sprintf("%-8s %-12s %-12s %-11s %s", id, inst.Version, target, state, bump))
	}
	if len(lines) == 0 {
		return []string{"no toolchains installed to check"}
	}
	return lines
}

func (m *Model) firstInstall(p sources.Product) (detect.Install, bool) {
	env := m.env()
	osName := m.osName()
	for _, id := range p.Tools {
		installs := detect.FindInstalls(catalog.GetTool(id), m.cfg, m.opts.Cache, false, env, osName)
		for _, in := range installs {
			if in.OK {
				return in, true
			}
		}
	}
	return detect.Install{}, false
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
