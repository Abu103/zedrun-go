# zedrun-go — Design (Spec)

Status: approved by delegation (user: "go with go lang … make sure its all good and
build that on diff folder not in this leave this as it is"). Decisions the user did not
make are marked "(my call)".

## 1. Intent

**Outcome.** A fast, dependency-free, single-binary "Code Runner for Zed" with a
first-class TUI, installable with one command — the compiled successor to the Python
`zedrun` (which stays exactly as it is, in `/home/zane/zedrun`).

**For whom.** Developers on Windows first, then macOS and Linux, who want
zero-install toolchain detection, one key to run the current file, and a UI that
feels good.

**Why now.** The Python version works but needs Python present. A static binary plus
a one-line installer (`irm … | iex` on Windows, `curl … | sh` on Unix) removes that
dependency, and a TUI makes the tool pleasant to live in. "Fast" is a real but
secondary benefit: zedrun spends its wall-clock inside the compiler it launches, so
the win is ~40 ms of startup, not perceived run speed.

**Success.**
1. `irm <url>/install.ps1 | iex` (Windows) or `curl -fsSL <url>/install.sh | sh`
   (Unix) installs one binary, no runtime deps.
2. `zedrun` with no args opens the TUI; `zedrun run FILE`, `zedrun install`, etc.
   work headless (scripts, CI, Zed tasks).
3. Feature parity with the Python zedrun: detect, run, which, doctor, outdated
   (stable/pre), use, pick, languages, clean, install/uninstall into Zed.
4. Config-compatible: reads the same `.zedrun.json` (JSONC, user + project).
5. Static binaries for linux/darwin/windows × amd64/arm64 published on a release.

**Said vs assumed.** Said: Go; binary executables; massgrave-style one-line install;
a TUI that feels good; a separate folder; leave the Python one untouched; plan first.
Assumed (my calls): Go 1.22+; `charmbracelet/bubbletea` + `lipgloss` for the TUI and
nothing else; GitHub Actions for the release matrix; the same JSONC config and the
same `// >>> zedrun` managed-block Zed integration; MIT licence.

## 2. Approaches considered

| | Pros | Cons |
|---|---|---|
| A. Pure stdlib, hand-rolled ANSI TUI | zero deps | crude UI; lots of fiddly terminal code |
| **B. Go + bubbletea/lipgloss (chosen)** | best-feeling TUI; still one static binary; tiny dep surface | two well-maintained charm deps |
| C. Rust + ratatui | smallest/fastest binary | slower to write; heavier toolchain; user chose Go |

B wins.

## 3. Architecture

Go module `github.com/zedrun/zedrun-go` (module path is my call; rename on publish).
One package per responsibility; `cmd/zedrun` is a thin entry.

| package | responsibility |
|---|---|
| `internal/platform` | OS name, config/cache/data/Zed dirs, PATH+PATHEXT scan, origin classification, WSL/short-path helpers |
| `internal/jsonc` | tolerant JSONC parse; offset-preserving comment blanking; managed-block insert/remove |
| `internal/config` | user+project `.zedrun.json`, validation, merge, set-preference |
| `internal/catalog` | data: tools, languages, runners (command templates), install hints |
| `internal/versions` | parse/compare versions (pre-release aware), judge installed vs latest |
| `internal/detect` | candidate discovery, probing, version extraction, probe cache, MSVC via vswhere |
| `internal/planner` | choose runner+install, evaluate conditions, expand templates into steps |
| `internal/executor` | run steps with PATH prefix, timing, footer, exit-code explanations |
| `internal/sources` | latest-version providers, HTTP fetcher with TTL cache, upgrade hints |
| `internal/installer` | edit Zed tasks.json/keymap.json via managed block; self-install paths |
| `internal/doctor` | diagnostics report (system/tools/languages/problems) |
| `internal/tui` | bubbletea model: dashboard, tool list, run view, update view |
| `internal/cli` | flag parsing + dispatch (bare path = run) |

Data flow for `run`: cli → config.Load → catalog.LanguageForPath → planner.Resolve
(→ detect) → planner.BuildPlan → executor.Execute.

## 4. Behaviour (parity, plus the TUI)

Detection, selection, planning/execution, config, version checking and the Zed
installer behave as the Python spec (`/home/zane/zedrun/docs/superpowers/specs/
2026-10-18-zedrun-design.md`, sections 4.1–4.6) — same candidate order, same
"usable only if `<path> <version-args>` exits 0 within 5 s" rule, same runner
placeholders (`{tool} {file} {dir} {stem} {name} {exe} {jar} {obj} {root}
{posix_file} {tool:ID}` and splices `{flags} {profile} {std} {libs} {args}`), same
managed-block text editing, same providers, same exit codes (child's on run; 127 no
toolchain; 1 bad input; 2 usage; 130 Ctrl-C).

**TUI (my call).** `zedrun` with no arguments launches it. A single-screen dashboard:
a header (OS, Zed detected or not, N toolchains found), a scrollable table of
languages with the default toolchain and version, and a footer of keys. Keys:
`r` run the file passed as an argument (or open a file picker), `d` doctor, `u`
updates (with a `p` toggle for pre-release), `t` pick a toolchain for the highlighted
language, `i` install into Zed, `q` quit. Every action streams its output into a
scrollable pane rather than leaving the TUI. Non-TTY stdin or `--no-tui`/piped output
falls back to plain text so the same binary works in scripts.

## 5. Distribution

- `install.sh` (Unix) and `install.ps1` (Windows) detect OS/arch, fetch the matching
  asset from the latest GitHub release, verify it runs (`zedrun --version`), and place
  it on PATH (`~/.local/bin` / `%LOCALAPPDATA%\zedrun` + user PATH). Idempotent;
  `--uninstall` reverses it. Hosted as raw files in the repo so the one-liners are
  stable.
- GitHub Actions `release.yml` cross-compiles `CGO_ENABLED=0` binaries for
  linux/darwin/windows × amd64/arm64 and attaches them to the tagged release.
- The binaries self-install into Zed (`zedrun install`), same managed-block edit.

## 6. Testing

`go test ./...`, table-driven. Fake executables (shell scripts that append to a
counter file) for detection/probe/lazy-validation tests. Golden files for each
version provider; HTTP is injected so no test touches the network. Parser and
managed-block tests mirror the Python suite's cases (CRLF, trailing commas,
comment-after-last-element, conflicting key). The TUI model is tested by driving
`Update` with synthetic messages (no real terminal).

## 7. Global constraints

Go 1.22+; `CGO_ENABLED=0` static binaries; dependencies limited to
`charmbracelet/bubbletea` + `charmbracelet/lipgloss`; config-compatible with the
Python zedrun; no network on `run`; build artifacts only under the cache dir; a user
file with comments is changed only via the `// >>> zedrun` managed block; MIT.
