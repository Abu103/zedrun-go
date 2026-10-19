// Package tui is the interactive dashboard. It drives the same core the CLI
// does, and every action's output lands in a scrollable pane rather than taking
// over the terminal. The model is pure: tests drive Update with synthetic
// messages and never need a real terminal.
package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/doctor"
	"github.com/zedrun/zedrun-go/internal/installer"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/planner"
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

	cfg  config.Config
	opts Options
	w, h int
}

// NewModel builds the dashboard model.
func NewModel(rows []LangRow, cfg config.Config, opts Options) Model {
	return Model{Rows: rows, cfg: cfg, opts: opts, w: 80, h: 24}
}

// Width and Height report the last window size seen.
func (m Model) Width() int  { return m.w }
func (m Model) Height() int { return m.h }

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update handles keys and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.Quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down", "j":
			if m.Cursor < len(m.Rows)-1 {
				m.Cursor++
			}
		case "p":
			m.Pre = !m.Pre
			m.append("channel: " + m.channelLabel())
		case "r":
			m.act("run")
		case "d":
			m.act("doctor")
		case "u":
			m.act("updates")
		case "t":
			m.act("pick")
		case "i":
			m.act("install")
		}
	}
	return m, nil
}

// View renders the dashboard.
func (m Model) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dim := lipgloss.NewStyle().Faint(true)
	sel := lipgloss.NewStyle().Reverse(true)

	var b strings.Builder
	b.WriteString(title.Render("zedrun-go — Code Runner for Zed"))
	b.WriteString("\n")
	b.WriteString(dim.Render(fmt.Sprintf("%s · %d languages · %s channel", m.osName(), len(m.Rows), m.channelLabel())))
	b.WriteString("\n\n")

	for i, r := range m.Rows {
		line := fmt.Sprintf("  %-12s %s", r.Language, r.Default)
		if i == m.Cursor {
			line = sel.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(m.Output) > 0 {
		b.WriteString("\n")
		for _, l := range m.Output {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dim.Render("r run · d doctor · u updates · p pre-release · t pick · i install · q quit"))
	return b.String()
}

// Run starts the dashboard. It returns the exit code to use.
func Run(cfg config.Config, opts Options) (int, error) {
	m := NewModel(Rows(cfg, opts), cfg, opts)
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

	rows := make([]LangRow, 0, len(keys))
	for _, k := range keys {
		def := "-"
		if res, err := planner.Resolve(k, "", cfg, opts.Cache, "", env, osName, nil); err == nil {
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
		m.append(fmt.Sprintf("pick: %s currently uses %s — run `zedrun-go use %s <tool>` to change it",
			row.Language, row.Default, row.Language))
	case "install":
		zed := platform.ZedConfigDir(m.env(), m.osName())
		bin, err := os.Executable()
		if err != nil {
			bin = "zedrun-go"
		}
		msgs, err := installer.Install(zed, bin, true, nil)
		if err != nil {
			m.append("install failed: " + err.Error())
			return
		}
		for _, s := range msgs {
			m.append(s)
		}
		m.append("(dry run — run `zedrun-go install` to apply)")
	}
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
