# zedrun-go dashboard — bug report

Scope: `internal/tui` (the `zedrun-go` dashboard, shown when the binary is run
with no arguments). The CLI and the core are **not** in scope — `run`, `which`,
`doctor`, `outdated`, `use`, `pick`, `install`, `uninstall`, `languages` and
`clean` were all exercised and behave correctly.

Every claim below is backed by a command you can re-run. The harness lives in
`internal/tui/bugrepro_test.go`:

```sh
go test ./internal/tui/ -run TestBug -v
```

It prints the current behaviour and passes, so the suite stays green while these
are open. **As you fix each one, convert the `t.Logf` into a real assertion** —
the logged numbers are the before-state.

---

## Summary

| # | defect | severity | where |
|---|--------|----------|-------|
| 1 | view is not windowed to the terminal height; content past the last row is unreachable | **high** | `tui.go:122-158` |
| 2 | output pane is append-only — no cap, no clear | **high** | `tui.go:147-153`, `tui.go:301-305` |
| 3 | no key scrolls the view | **high** | `tui.go:90-117` |
| 4 | no mouse handling, so the wheel is ignored | medium | `tui.go:79-119` |
| 5 | long lines are not clamped to the terminal width | medium | `tui.go:138-153` |
| 6 | `r`, `t` and `i` do not do what the UI says they do | **high** | `tui.go:204-245` |
| 7 | the dashboard never persists the probe cache | low | `cli.go` `cmdTUI` |

The root cause of 1-4 is the same: **`View()` renders the whole model, and the
model has no notion of a viewport.** `m.w` and `m.h` are stored
(`tui.go:87-89`) and exposed (`tui.go:62-63`) but are never read by `View()`.

---

## 1. The view is not windowed — this is the "hijacked scroll"

`tea.WithAltScreen()` (`tui.go:164`) switches the terminal to the alternate
screen buffer. In xterm-compatible terminals **the alternate screen has no
scrollback**: while the dashboard is up, the wheel has nothing to scroll and
your terminal's own history is out of reach. That is expected for a TUI, and it
is only a problem because the app then provides no way to scroll *itself*.

`View()` writes every language row and every line of `m.Output` with no bound:

```go
for i, r := range m.Rows { ... }          // tui.go:138-145
if len(m.Output) > 0 { ... }              // tui.go:147-153
```

Evidence — three presses of `d` in an 80x24 terminal:

```
terminal height 24 -> View() emits 154 lines (130 unreachable)
key-hint footer visible in the first 24 rows: false
```

Two consequences:

- **130 lines are drawn but unreachable.** The terminal only shows the first 24;
  the rest are written below the fold and cannot be scrolled to, by key or by
  wheel.
- **The key hints disappear.** The footer (`tui.go:155-156`) is written last, so
  after a single `d` the line telling you which keys exist is off-screen — and
  there is no way to get it back without quitting and restarting.

**Suggested fix.** Add a viewport: keep a `scroll` offset in the model, clamp
`View()` to `m.h` rows, reserve the last row for the footer so it is always
visible, and render a windowed slice of the body. The body is
`len(m.Rows) + len(m.Output)` lines; the window is `m.h - headerRows - 1`.
Clamp `scroll` to `max(0, bodyLen-window)` on every append and on resize.

## 2. The output pane is append-only

`append` (`tui.go:301-305`) only ever adds. Nothing truncates it and no key
clears it, so pressing `d` five times gives:

```
Output length after each 'd': [49 98 147 196 245]
```

Each `d` re-appends the entire doctor report. Combined with bug 1 this makes the
dashboard unusable after a couple of keypresses, and it grows without bound for
as long as the session lasts.

**Suggested fix.** Cap the pane (a ring buffer of, say, 2000 lines), and/or have
each action replace its own previous output rather than appending. Add a key to
clear it (`c`) and mention it in the footer.

## 3. No key scrolls the view

```
pgup/pgdown/home/end/ctrl+u/ctrl+d/ctrl+b/ctrl+f/g/G changed the view: false
```

The `Update` key switch (`tui.go:90-117`) handles only `q`/`esc`/`ctrl+c`,
`up`/`k`, `down`/`j`, `p`, `r`, `d`, `u`, `t`, `i`. `up`/`down` move the
*language cursor*, not the view.

**Suggested fix.** Bind `pgup`/`pgdown`, `ctrl+u`/`ctrl+d` and `home`/`end` to
the scroll offset, and keep `up`/`down` for the cursor. Once the cursor moves
outside the window, scroll the window to follow it.

## 4. No mouse handling

```
wheel events changed the view: false
```

`Update` has no `tea.MouseMsg` case (`tui.go:79-119`) and `Run` does not enable
mouse reporting (`tui.go:164` — verified: the process emits no `1000h`/`1002h`/
`1003h`/`1006h` sequence). So the wheel is inert while the alternate screen is
already withholding the terminal's scrollback.

**Suggested fix.** Either enable `tea.WithMouseCellMotion()` and handle
`MouseButtonWheelUp`/`WheelDown`, or leave the mouse alone but make sure keys
cover everything (bug 3). Enabling the mouse without handling it would be worse
than today.

## 5. Lines are not clamped to the terminal width

```
terminal width 80 -> widest rendered line 117
worst:   data_dir    /home/zane/.hermes/cache/scratch/.../001/.local/share/zedrun
```

`View()` writes each line verbatim. Anything longer than `m.w` wraps in the
terminal, which adds rows the model does not know about — so the rendered height
no longer matches what `View()` believes it emitted, and the layout below shifts.

**Suggested fix.** Truncate or hard-wrap each line to `m.w` before writing it.
`lipgloss.NewStyle().MaxWidth(m.w)` does this, or use `ansi.Truncate`.

## 6. Three advertised actions do not act

The footer and the README advertise these as doing something. All three only
print a message:

```
act("run")     -> run: open a file in Zed and press F6, or pass a path to zedrun-go
act("pick")    -> pick: c currently uses gcc — run `zedrun-go use c <tool>` to change it
act("install") -> would write .../tasks.json | would write .../keymap.json | (dry run — run `zedrun-go install` to apply)
```

- `act("run")` (`tui.go:207-213`) never runs anything. It cannot: the model has
  no file to run unless `opts.File` was passed, and there is no file picker.
- `act("pick")` (`tui.go:225-228`) prints the current default and tells you to
  use the CLI. It does not list the alternatives or let you choose.
- `act("install")` (`tui.go:229-244`) calls `installer.Install(..., true, nil)`
  with `dryRun = true` hard-coded, so `i` can never install anything.

**Suggested fix.** Either implement them or stop advertising them. The smallest
honest change is to relabel the footer so it says these open a prompt rather than
perform the action. Implementing `pick` is the highest-value of the three: the
data is already there (`detect.FindInstalls`) and `use --path` is the write path
(bug 6 in the README's history — the tool id cannot disambiguate two installs,
so the picker must write a path).

## 7. The dashboard never persists the probe cache

Every CLI command calls `cache.Save()`; `cmdTUI` does not (`internal/cli/cli.go`,
`cmdTUI`):

```
grep -c 'cache.Save' in cmdTUI -> 0
```

So toolchain probes run in the dashboard are thrown away and re-discovered on
the next launch. Cheap to fix: save after `tui.Run` returns, or after the
initial `Rows()` scan.

---

## Checked and found NOT to be bugs

Recorded so these are not chased:

- **`q` exits cleanly.** Verified over a real pty: the process exits ~0.1s after
  the keypress and emits both `ESC[?1049l` (leave alternate screen) and
  `ESC[?25h` (restore cursor). The terminal is left in a good state. An earlier
  capture appeared to show a missing `1049l`/`25h` — that was `timeout` killing
  the process with SIGKILL, not a defect.
- **Cold start is fast.** 681 ms to resolve all 25 languages against the real
  `PATH` with an empty probe cache (`ZEDRUN_PROBE_REAL=1 go test ./internal/tui/
  -run TestBug7 -v`). The 27 s observed earlier was wall-clock time spent sitting
  in the dashboard, not startup.
- **`p` works.** It toggles the channel label. It looked broken in a raw pty
  capture only because bubbletea redraws just the changed cells, so the new label
  never appears as a contiguous string in the byte stream.
- **The language table and cursor** (`up`/`down`/`k`/`j`) work, including the
  cursor reset when the table shrinks after the async scan lands.
- **The async scan** (`Init` → `rowsMsg`) works: the first frame paints
  immediately with "scanning toolchains…" and the table fills in when ready.

---

## Suggested order

1. Bug 1 (viewport) — it is the root of the complaint and of bugs 2-5's impact.
2. Bug 6 (`pick`) — the only advertised action with a clear, small implementation.
3. Bug 2 (cap the pane), bug 3 (scroll keys), bug 5 (clamp width).
4. Bug 4 (mouse) and bug 7 (cache save).

`internal/tui/tui_test.go` has the existing dashboard tests; keep them green.
The model is deliberately pure — no terminal needed — so all of this is
testable by driving `Update` with synthetic messages, which is how
`bugrepro_test.go` gathers its evidence.
