# UI/UX Revamp Implementation Plan

> **For agentic workers — the handoff is a FREE full-featured Claude (web, sandboxed).** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Redesign the `zedrun-go` dashboard TUI (charmbracelet/bubbletea + lipgloss) from a dense, wrapped chart into a clean, massgrave-style screen: every key press clears the terminal and redraws a single focused page in milliseconds, with readable fonts, generous padding, and less clutter — while keeping every existing key binding (`r run · d doctor · u updates · U update · t pick · g get · i zed setup · c clear · m mouse · q quit`) and the `U` updates overlay.

**Architecture:** Rework the `internal/tui` model + render layer. Replace the current single-scrollable-body dashboard with a full-screen "one page per action" model: every key press clears the screen and redraws exactly one processed view. Keep the same actions, tools, and data sources — only the presentation changes. A new rendering layer (styling + padding + page model) replaces the dense chart.

**Tech Stack:** Go 1.24 (floor from `go.mod`); charmbracelet/bubbletea + charmbracelet/lipgloss (existing in `internal/tui`); ANSI wrapping via `github.com/charmbracelet/x/ansi`.

**Planning note:** This is the UI/UX deliverable for the toolchain-assist revamp (see `docs/superpowers/specs/2026-10-22-toolchain-assist-revamp-design.md` for the toolchain spec). The TUI job here is the *presentation* layer.

## Global Constraints
- `go 1.24.0` floor from `go.mod`; `make dist` stays 6 static binaries (linux+darwin+windows × amd64+arm64, CGO_ENABLED=0). New code must not raise the floor or drop any target.
- `go test -race -count=1 ./...` must stay green after any model/view change; existing `internal/tui` tests must pass.
- Keyboard-first, no mouse dependency: `m` toggles mouse capture (as before); every key still drives the same actions.
- Keep `internal/provision`, `internal/config`, `internal/catalog`, `internal/doctor` untouched — the TUI reads their outputs only.
- A free full-featured Claude (API/web) is sandboxed: it cannot run commands on the user's machine. It drives the user with copy-paste instructions and verifies by reading outputs back.

## Review Focus
- **Readability on a small terminal:** the old chart wraps long lines and the status bar eats the first line. New TUI uses wrapped text (not truncation) and a status bar that never overlaps the body; the table is padded, larger, and one column of `Language → Default` per row.
- **Padding / the "too cluttered" complaint:** the old dashboard pins a header + a status bar + a footer hint and fills the gap with a scrollable 2000-line output accumulator. The new TUI removes the accumulator, floats each action's output in a compact block, and lets the user clear it (`c`) with a single clear-redraw.
- **The massgrave redraw:** every key press clears the page and redraws exactly one processed view (~ms), instead of repainting a partial screen while an overlay rotates.
- **Testability:** the model is pure — `Update` + `View` + synthetic `tea.KeyMsg`s drive it, so a redesign is proven by the same `tui_test.go` class (key actions → model state → `View()`).

---
