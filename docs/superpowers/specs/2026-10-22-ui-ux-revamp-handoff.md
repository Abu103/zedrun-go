# UI/UX Revamp — Handoff for the free full-featured Claude (Google web, sandboxed)

> You are handed a zip of the `zedrun-go` repo at the toolchain-assist implementation (commit `0963875` plus the toolchain package) **plus** this document. You are a **full-featured Claude with an API and a browser**, running in a sandbox. Your sandbox either has network access or not — check first. You run commands inside your sandbox, which is the environment you verify in. You never run anything on the user's real machine yourself.

## Goal
Turn `zedrun-go`'s dashboard TUI (charmbracelet/bubbletea + lipgloss) from a dense, wrapped chart into a clean, massgrave-style screen: every key press clears the terminal and redraws a single focused page in milliseconds, with readable spacing, bold emphasis, colour, and casing — no impossible "larger font size" (a terminal has one font size; you can only change spacing, bold, colour, casing). Keep every key binding (`r run · d doctor · u updates · U update · T toolchain picker · t pick · g get · i zed setup · c clear · m mouse · q quit`).

## Why
The current dashboard is cluttered: a 2000-line scrollable output accumulator, wrapping lines, a header + status bar + footer hint pinned into the gap. The user reports they cannot read anything. The fix is *display-only* — the toolchain engine (`internal/provision`, `catalog`, `config`, `doctor`, `internal/toolchain`) keeps producing the same data; only the TUI presentation changes.

## Design (granted)
1. **Massgrave model:** every key handler renders its result into the model, then calls `View()` which clears the terminal and draws exactly one processed view. No accumulation, no state carried across keys.
2. **No accumulator for the main table:** the 2000-line output scroll is removed for the dashboard table; each action replaces the last.
3. **Bounded output streams:** `cappedBuffer` / `maxOutput = 2000` still back the *install* and *update* streams (tui.go lines ~1022 and ~1173). Keep a small capped tail (e.g. ~4096 bytes) for those streams — they need scrollback — and replace the pane contents per action for the main dashboard.
4. **Readable typing:** the terminal has one font size, so you can change only **spacing, bold, colour and casing**. Use those to create hierarchy and whitespace.
5. **One page per action:** dashboard = one compact table + one Output block, status bar at bottom, one-line footer hint.
6. **Overlays** (`picker`, `getMenu`, `updMenu`, `pathPrompt`) in the same full-screen single-action style.
7. **Keyboard-first:** `m` toggles mouse capture (existing); every key still drives the same action. Keep `tea.Program` + `tea.WithAltScreen`.

## What the user's machine looks like
- The repo is at the toolchain-assist implementation; `go 1.24.0` floor; `make dist` builds 6 static binaries (linux+darwin+windows × amd64+arm64, CGO_ENABLED=0).
- Existing tests: `go test -race -count=1 ./internal/tui/...` must stay green; `go test -race ./...` must stay green; `gofmt -l ./internal/tui/` must be clean; `go vet ./...` must be clean.
- New files: `internal/tui/model.go` (slim model: Mode, rows, one Output block, window size), `internal/tui/view.go` (clean render: clear screen, one page, padded typography), `internal/tui/overlays.go` (the overlays in the same full-screen style). `tui_test.go` / `ui_test.go` / `actions_test.go` / `update_test.go` assert the old layout — update those where they assert the old layout; add `internal/tui/view_test.go` proving clear-on-redraw, one-line output, and single-line status.

## Your job
1. Read `internal/tui/tui.go` + `internal/tui/tui_test.go` (and the `_test.go` files) to learn the existing actions and model.
2. Implement the redesign as above (model + view + overlays), preserving every action and key, including `T` (toolchain picker).
3. Update the old `_test.go` files where they assert the old layout; add `internal/tui/view_test.go`.
4. Verify with your own sandbox toolchain: `gofmt -l ./internal/tui/`, `go build ./...`, `go vet ./...`, `go test -race -count=1 ./internal/tui/...`, then `go test -race ./...`, then `make dist`.
5. Show the user your verification output, then hand it back.

## Rules for you
- Do not install, build, or run on the user's real machine yourself. Your sandbox is the environment you verify in; if your sandbox lacks network and cannot fetch Go, state that clearly and do not promise a compile.
- If you need the user to run `make dist` or a `go test` command, give exact, copy-paste-able shell commands and ask them to paste the output back.
- Never echo credentials, API keys, tokens, or connection strings.
- If a step is ambiguous, state it plainly and ask the user.

---
For the builder doing the work: run `gofmt -l ./internal/tui/`, then `go build ./...`, `go vet ./...`, `go test -race -count=1 ./internal/tui/...`, `go test -race ./...`, then `make dist` — all must return green before you report done.
