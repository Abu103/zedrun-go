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

	confirmInstall bool // set after an `i` preview; a second `i` applies

	// picker is non-nil while the tool picker overlay is open.
	picker *picker

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
	case tea.MouseMsg:
		if m.picker != nil {
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
		case "p":
			m.Pre = !m.Pre
			m.append("channel: " + m.channelLabel())
		case "r":
			m.LastAction = "run"
			if m.opts.File != "" {
				bin, err := os.Executable()
				if err != nil {
					bin = "zedrun-go"
				}
				// ExecProcess hands the terminal to the child and restores the TUI after.
				return m, tea.ExecProcess(exec.Command(bin, "run", m.opts.File),
					func(err error) tea.Msg { return runDoneMsg{err} })
			}
			m.act("run")
			m.Scroll = m.bodyLen()
		case "d":
			m.act("doctor")
			m.Scroll = m.bodyLen()
		case "u":
			m.act("updates")
			m.Scroll = m.bodyLen()
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
			lines = append(lines, m.fit(l))
		}
	}
	return lines
}

func (m Model) bodyLen() int {
	n := len(m.Rows)
	if m.Rows == nil {
		n = 1
	}
	if len(m.Output) > 0 {
		n += 1 + len(m.Output)
	}
	return n
}

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

// fit truncates a line to the terminal width so it never wraps.
func (m Model) fit(s string) string {
	if m.w <= 0 {
		return s
	}
	return ansi.Truncate(s, m.w, "…")
}

// View renders the dashboard, windowed to the terminal: header, a slice of the
// body, and a footer that is always on the last row.
func (m Model) View() string {
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
	sub := fmt.Sprintf("%s · %d languages · %s channel", m.osName(), len(m.Rows), m.channelLabel())
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
	b.WriteString(m.fit(dim.Render("r run · d doctor · u updates · p pre · t pick · i install · c clear · q quit")))
	return b.String()
}

// Run starts the dashboard. It returns the exit code to use. The toolchain scan
// runs inside the program, so the first frame is on screen immediately.
func Run(cfg config.Config, opts Options) (int, error) {
	m := NewModel(nil, cfg, opts)
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
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

func (m *Model) act(name string) {
	m.LastAction = name
	switch name {
	case "run":
		file := m.opts.File
		if file == "" {
			m.append("run: open a file in Zed and press F6, or pass a path to zedrun-go")
			return
		}
		m.append("run: " + file + " (use zedrun-go run for full output)")
	case "doctor":
		report := doctor.Collect(m.cfg, m.opts.Cache, m.env(), m.osName(), nil)
		var b strings.Builder
		doctor.Render(report, &b, false)
		for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
			m.append(line)
		}
	case "updates":
		for _, line := range m.updateLines() {
			m.append(line)
		}
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
