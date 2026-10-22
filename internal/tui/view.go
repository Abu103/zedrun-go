package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// outputPad is the left margin of the output block. Output is wrapped to the
// width left after it, so a padded line never exceeds the terminal.
const outputPad = "  "

// startsAction reports whether a dashboard key starts an action. Those keys
// replace the previous action's output; navigation and scroll keys do not.
func startsAction(key string) bool {
	switch key {
	case "r", "d", "T", "u", "U", "t", "g", "i", "p", "m":
		return true
	}
	return false
}

// clearing wraps Model so every key press clears the terminal before the next
// frame is drawn: one page per action, nothing left from the last one. The
// clear lives here rather than in Model so the model stays pure and the tests
// keep driving it directly.
type clearing struct{ Model }

// Update delegates to the model and asks the program to clear the screen
// after any key press, except the one that quits.
func (c clearing) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := c.Model.Update(msg)
	m, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if _, isKey := msg.(tea.KeyMsg); isKey && !m.Quitting {
		cmd = tea.Batch(tea.ClearScreen, cmd)
	}
	return clearing{m}, cmd
}
