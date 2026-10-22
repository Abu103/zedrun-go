# UI/UX Revamp Design — massgrave-style TUI

> **For agentic workers:** The executor is a free full-featured Claude account (web, sandboxed). It reads this spec + a zip of the repo and implements the TUI redesign into `internal/tui`. It cannot run commands on the user's machine; it verifies by reading the user's terminal output back.

## Intent and the why
The current `internal/tui` dashboard (charmbracelet/bubbletea + lipgloss) is a dense chart: a scrollable output accumulator, wrapping lines, a header + status bar + key-hint footer pinned into the gap. The user reports it is **too cluttered** and reads nothing. The fix is a *display-only* revamp — no change to the core toolchain engine (`internal/provision`, `catalog`, `config`, `doctor`) — that turns the dashboard into a clean, massgrave-style screen: every key press clears the terminal and redraws a single focused view in milliseconds.

This is not a framework change. It is the same bubbletea/lipgloss app, rebuilt on a **full-screen, one-page-per-action** model with generous padding, clearer typography, and a single-source-of-truth overlay system.

## Goals
- **Clear on redraw:** each key action clears the screen and redraws one processed view in ~milliseconds; no partial repaint, no accumulated scrollback.
- **Readable:** generous padding, wrapped text (never truncation), and spacing/bold/colour/casing for hierarchy — a terminal has one font size, so font size is not changeable.
- **Less clutter:** no dense chart; the actionable view is a single block; a compact status line; one page per action.
- **Backward compatible:** every key still drives the same action (`r run · d doctor · u updates · U update · t pick · g get · i zed setup · c clear · m mouse · q quit`).
- **Testable:** the model stays pure — `Update` + `View` + synthetic `tea.KeyMsg`s drive it, so the redesign is covered by the existing `tui_test.go` class.

## Non-goals
- No framework change; no dependency added (bubbletea + lipgloss only).
- No changes to `internal/provision`, `catalog`, `config`, `doctor` (they keep producing the same data).
- Not a rewrite from scratch: the existing actions, overlays, install, pick, update, and diagnostic logic are preserved, just re-presented.

## Anti-goals
- No per-key scrollback accumulation.
- No dense wrapping that clips the actionable tail of a line.
- No permanent overlays that eat the screen between presses.
- No changing the user's data model (toolchain install, version, preference).

## Design decisions

### 1. New model layer — one page per action
- A `Model` holds only the state a *view* needs: the current `Mode` (dashboard, overlay, prompt, status), the active rows, a single `Output` block, and the window size.
- Every key handler (the `case` arms in `Update`) renders its result into the model, then calls `View()`, which **clears the terminal and draws exactly one processed view**. No accumulation, no state carried across keys.

### 2. Rendering — massgrave styling
- Uses the existing lipgloss `Style`s but rebuilt with spacing, bold, colour and casing (a terminal has one font size, so font size is not changeable): generous vertical padding, clear type scale, a single accent colour.
- The dashboard page is a **single table** (`Language → Default`) plus one **Output** block on top, with the status bar at the bottom. No scrollable 2000-line accumulator.
- Overlays (`picker`, `getMenu`, `updMenu`, `pathPrompt`) reuse the same frame: full-screen, single action, with the keys the user must press.

### 3. No accumulation
- Action output is rendered once, inside the page, and replaced on the next key. The 2000-line `cappedBuffer` is removed for the main table (each action replaces the last).

### 4. Status bar
- One compact line: selected tool, `updates` count (when known), a short status hint. Never overlapping the body; the footer hint gives the keys.

### 5. Footer
- A single, thin key-hint line pinned to the last row of the window (not a wrapping chart).

## The parts of how (files changed by the builder)
1. `internal/tui/model.go` — a slim `Model` (Mode, rows, one Output block, window size, selected row).
2. `internal/tui/view.go` — clean rendering: clear screen, one page, padded typography, thin footer.
3. `internal/tui/overlays.go` — the four overlays (`picker`, `getMenu`, `updMenu`, `pathPrompt`) in the same full-screen style.
4. `internal/tui/view_test.go` — new tests proving the redesign: the view clears the screen on each key, output is one block (not 2000 lines), and the status bar is single-line.

## The build stencil (what the executor implements)
1. New `model.go` + `view.go` + `overlays.go` (and tests in `internal/tui`).
2. Update `NewModel`, `Update`, `View`, `Run`; keep `tea.Program` and `tea.WithAltScreen` (existing).
3. Verify: `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test -race -count=1 ./internal/tui/...`, then `go test -race ./...`, then `make dist`.

## Stack
- Go 1.24 (floor from `go.mod`); bubbletea + lipgloss for the existing TUI; no new dependency.

## Global constraints
- `go 1.24.0` floor; `make dist` stays 6 static binaries. New code must not raise the floor or drop any target. Tests: `-race -count=1 ./internal/tui/...` green (and `./...`).
- Keyboard-first; `m` toggles mouse capture; every key drives the same action.
- `internal/provision`, `catalog`, `config`, `doctor` untouched.

## Review Focus
- **Readability on a small terminal:** single column `Language → Default`, padded, no wrapping or truncation except where the output block *must* wrap (the actionable tail stays visible).
- **Padding / clutter:** each action renders one compact block; a single compact status line; a one-line footer hint. No 2000-line accumulator.
- **massgrave redraw:** full-screen clear + single-view redraw on every key, ~ms; the status bar and footer stay put.
- **Testability:** model is pure; `Update` + `View` + synthetic `tea.KeyMsg`s; same `tui_test.go` class as before, plus new `view_test.go` proving clear-on-redraw, one-line output, and single-line status.

---
