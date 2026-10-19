# zedrun-go — repository detail

A single document describing this repository, for an external review. It states
what the software is, how it is built and tested, the decisions behind it, and —
deliberately — the parts that are weakest or unverified, so a reviewer can spend
its attention there rather than re-deriving the obvious.

Everything in the "measured" sections was produced by running a command on this
machine, and the command is given so it can be re-run.

---

## 1. At a glance

| | |
|---|---|
| What | A code runner for the [Zed](https://zed.dev) editor, as one static binary |
| Language | Go (module `github.com/zedrun/zedrun-go`) |
| Go floor | `go 1.24.0` in `go.mod` — verified by building the full suite on exactly that toolchain |
| Runtime dependencies | none; the binary is `CGO_ENABLED=0` and statically linked |
| Go dependencies | `charmbracelet/bubbletea v1.3.10`, `charmbracelet/lipgloss v1.1.0`, `charmbracelet/x/ansi v0.10.1` — nothing else direct |
| Size | 42 Go files; 5,755 lines of source, 4,645 lines of test; 250 test functions |
| Packages | 15 under `internal/`, plus `cmd/zedrun` |
| Languages supported | 24 |
| Release targets | 6 — `{linux,darwin,windows}` × `{amd64,arm64}` |
| Commits | 29 on branch `impl` |
| Git remote | **none configured** — this is a local repository |
| Licence | MIT |

Test status at the time of writing:

```
$ go test -count=1 ./...
ok  cmd/zedrun  0.008s          ok  internal/executor   1.325s
ok  internal/catalog   0.019s   ok  internal/installer  0.014s
ok  internal/cli       6.465s   ok  internal/jsonc      0.006s
ok  internal/config    0.012s   ok  internal/planner    0.018s
ok  internal/detect    1.341s   ok  internal/platform   0.008s
ok  internal/dist      0.497s   ok  internal/sources    0.007s
ok  internal/doctor    0.215s   ok  internal/tui        2.229s
ok  internal/versions  0.003s   ?   internal/version    [no test files]
```

`gofmt -l .` prints nothing and `go vet ./...` is clean. `go test -race ./...`
is also clean.

---

## 2. What it does

Zed can run a file through a task, but a task is a fixed shell command — it does
not know whether you have `python3` or `python`, whether your `gcc` is the MSYS2
one or the Visual Studio one, or whether the Go on your `PATH` is the one you
meant. zedrun-go answers that and then runs the file.

1. **Detect** every install of every toolchain it knows about, with a version and
   an origin (`system`, `msys2`, `llvm`, `visual-studio`, …).
2. **Resolve** which install should run a given file — by language, by the file's
   extension or shebang, by any preference you have pinned, then by a fixed
   priority order.
3. **Run** it, returning your program's own exit code and passing its stdin,
   stdout and stderr straight through.
4. **Integrate** with Zed by writing tasks and keybindings into Zed's own
   `tasks.json` / `keymap.json`, inside managed blocks it can later remove.

It also ships a TUI dashboard (run with no arguments) that shows the language
table, runs diagnostics, checks for updates, and lets you pin a toolchain.

---

## 3. Build, test, run

The Go toolchain is not installed system-wide on this machine; it lives at
`~/.local/go`.

```bash
export PATH="$HOME/.local/go/bin:$PATH"
cd /home/zane/zedrun-go

go build -o /tmp/zedrun-go ./cmd/zedrun   # single binary
go test -count=1 ./...                    # full suite
go test -race -count=1 ./...              # concurrency
gofmt -l .                                # must print nothing
go vet ./...                              # must be clean

make dist                                 # all six release targets into dist/
make resources                            # regenerate the Windows .syso files
```

`make dist` builds with `CGO_ENABLED=0` and `-trimpath -ldflags "-s -w"` for
`{linux,darwin,windows}` × `{amd64,arm64}`. Verified output:

```
$ file dist/zedrun-go-linux-amd64
dist/zedrun-go-linux-amd64: ELF 64-bit LSB executable, x86-64, statically linked, stripped
```

---

## 4. Layout

| Package | Responsibility |
|---|---|
| `cmd/zedrun` | `main` — three lines: `os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))` |
| `internal/cli` | Argument parsing, dispatch, exit codes, per-command `--help` |
| `internal/catalog` | Data tables: tools (names, version args, install dirs), languages (extensions, shebangs, runner steps) |
| `internal/config` | `.zedrun.json` load/merge, preferences, the layered config resolution |
| `internal/platform` | All OS-dependent path and environment logic. Every function takes `(env map[string]string, osOverride string)` |
| `internal/detect` | Toolchain discovery: candidate paths, probing, the version cache |
| `internal/planner` | Chooses the toolchain for a file, and builds the plan of steps to run it |
| `internal/executor` | Runs a plan, with timeouts and exit-code propagation |
| `internal/versions` | Version parsing, comparison, and judging installed-vs-latest |
| `internal/sources` | Upstream version providers (one per feed shape) with an on-disk cache |
| `internal/doctor` | Diagnostics report (human and `--json`) |
| `internal/installer` | Writes/removes the Zed tasks and keymap entries |
| `internal/jsonc` | Comment-preserving editing of Zed's JSONC files, via managed blocks |
| `internal/tui` | The bubbletea dashboard |
| `internal/dist` | Tests of the distribution surface: installers, workflows, docs |
| `internal/version` | The version string constant |

Roughly 1:1 test-to-source ratio, concentrated where the risk is: `tui` (904
test lines), `cli` (649), `platform` (388), `versions` (343).

---

## 5. Design decisions worth reviewing

These are the choices most likely to be wrong, so they are stated with their
reasoning rather than left implicit.

**Every OS-dependent function takes the OS and the environment as parameters.**
`func CacheDir(env map[string]string, osOverride string) string`, where an empty
`osOverride` means the real host. This is what makes the Windows and macOS logic
testable from Linux. It was done from the first function, not retrofitted.

**HTTP is injected as `func(string) ([]byte, error)`.** Every version provider is
tested against fixtures; no test touches the network.

**The bubbletea model is pure.** Tests drive `Update` with synthetic messages and
never start a program. `View` has a value receiver and reads the stored window
size.

**Detection caches at two levels.** A per-run `DirCache` memoises directory
listings (probing 30 tools would otherwise re-list the same directories 30 times
— on WSL with a Windows `PATH` that was 6.9s of `stat` calls, now 0.2s), and a
`ProbeCache` memoises version probes on disk keyed by path+mtime+size.

**A candidate is only "usable" if it actually runs.** Each candidate is executed
with its version arguments and must exit 0 in time. This rejects Windows Store
stubs and broken installs rather than reporting them as available.

**`versions.ParseStrictVersion` exists alongside `ParseVersion`.** `ParseVersion`
reads a version out of arbitrary banner text, which is right for `gcc --version`
output and wrong for a directory listing: python.org's FTP index lists `win32`
beside `3.14.7`, and `ParseVersion` cheerfully read the `32` out of `win32` and
reported it as the newest release. Listings and release tags use the strict form.

**Version providers never promote a pre-release to stable.** JetBrains tags Kotlin
dev builds as `build-2.5.20-dev-1642`; when a feed contained only pre-releases the
earlier code promoted the newest to "stable" and advertised a dev build as the
latest release. It now falls back to `/releases/latest`, which answers the
question exactly.

**Managed blocks, not JSON rewriting.** Editing `tasks.json` means inserting into
a JSONC file the user also edits by hand. Rather than parse and re-serialise
(which would destroy comments and formatting), the installer writes between
`// >>> zedrun-go` and `// <<< zedrun-go` markers and only ever touches that
region. Comma placement uses the comment-blanked text, not the raw text — see
`internal/jsonc`.

---

## 6. Command surface

```
run        run a file            run FILE [args…] [--release] [--tool T] [--timeout S]
which      show the toolchain a file would use
doctor     diagnostics           [--json] [--sources] [--all]
outdated   installed vs latest   [--json] [--pre] [--fail-on-outdated]
use        set a language preference   use LANGUAGE TOOL [--path P] [--config C]
pick       choose a toolchain for a language
install    install into Zed      [--dry-run] [--zed-dir D]
uninstall  remove from Zed       [--purge]
languages  list supported languages
clean      clear the zedrun-go cache
```

Running with no command opens the TUI. `zedrun-go <command> --help` works for
every command, and `--` stops flag parsing so `run file -- --help` forwards
`--help` to the program being run.

**Exit codes.** Your program's own code on a run; `124` killed by `--timeout`;
`127` no toolchain found; `1` bad file or `--fail-on-outdated` found something;
`2` usage error; `130` interrupted.

**Network.** Only `outdated` and `doctor --sources` make requests, and results
are cached on disk (6h TTL) so later runs work offline.

---

## 7. Testing approach

- Fake executables are shell scripts written into `t.TempDir()` with mode `0755`.
  A script that appends to a counter file proves a probe was served from cache
  rather than re-run.
- The environment is injected as a map, never `os.Setenv`, so tests cannot leak
  state into each other.
- `internal/dist` tests the things the Go suite otherwise cannot reach: that
  `install.sh` actually downloads, verifies and installs a stub asset (driven
  through `ZEDRUN_BASE_URL=file://…`), that it refuses a binary that will not run,
  that the release workflow covers all six targets, and that the docs still
  advertise the one-line install.
- TUI tests assert on the rendered frame, not only the model: `View()` must emit
  exactly the terminal height, the footer must be on the last row, and no line may
  exceed the width. A pure-model suite passes happily while the dashboard draws
  off the bottom of the screen.

---

## 8. Verification beyond the test suite

The test suite is not the whole evidence. The following were measured directly,
and each is reproducible.

**The real binary was driven in a pty.** Model tests never render a frame; the
frame is what the user sees. Confirmed on a 110×40 terminal: alt-screen entered
and left (`1049h`/`1049l`), cursor hidden and restored (`25l`/`25h`), footer on
the last row, `PgUp` and the wheel both redraw, `q` exits cleanly.

**The Windows binary was run on real Windows.** The `.exe` was executed both
through WSL interop and from `cmd.exe` on the user's machine, and its output read.
This is how the two most serious defects were found (§9).

**Timings were measured on the path production takes**, not on a convenient one:

| operation | measurement |
|---|---|
| `doctor` cold (per-run cache empty) | 886 ms |
| `doctor` warm | 53 ms |
| `doctor` with no cache at all (test-only path) | 13.9 s |
| update check (warm source cache) | 315 ms |
| toolchain scan, 25 languages | ~700 ms |

The 13.9 s figure is a configuration `cmdTUI` never produces — it always passes a
cache — so the decision to move `doctor` off the UI thread was made on 886 ms, not
on 13.9 s.

---

## 9. The last four commits

These fixed defects found by running the built Windows binary. They are the least
settled part of the repository.

**`aa03507` — the dashboard's scroll was hijacked.** `View()` was not windowed to
the terminal height, so with the alternate screen there was no scrollback to fall
back on and content past the last row was unreachable. Six defects were written up
in `docs/BUG-REPORT.md` with a reproducing harness in
`internal/tui/bugrepro_test.go`. Fixed: windowed body, capped and clearable output
pane, scroll keys, mouse handling, width clamping, and the picker overlay.

**`f9052f0` — two dead keys, and the slow work moved off the UI thread.** `u` did
nothing because `cmdTUI` passed a nil `Fetcher` and nothing ever set `Options.File`
either, so `r`'s first fallback was unreachable code. Both fixed, plus `--file`.
`u` and `d` now run as `tea.Cmd`s with a progress line. Mouse capture is off by
default (it blocks text selection) with `m` to toggle. Output wraps rather than
truncates, because the actionable end of a line — a flag, a path — is on the
right. This commit also fixed a **data race** it exposed: `ProbeCache` had no
mutex, so its lazy `DirCache` init raced once more than one goroutine probed, and
each goroutine silently built its own cache, losing the sharing that makes doctor
fast. Found with `-race` (8 reports), now guarded, with
`internal/tui/race_test.go` as the regression guard.

**`1e8216e` — three Windows bugs.**

1. *The Windows build was blind to most of the machine.* Windows stored the
   environment variable as `path`; the code read `env["PATH"]`, an exact-case map
   lookup, which returned `""`, so the entire `PATH` was skipped. Measured before
   and after on the same machine: 11 tools found → 20. `python`, `go` and `node`
   are only reachable through `PATH`, which is why `pick` said "no installed
   toolchains found for python" on a machine with three Pythons. Fixed with
   `platform.Env`, exact on Unix (where `PATH` and `path` really are different
   variables) and case-folding on Windows; applied to `PATHEXT`, `USERPROFILE`,
   `HOMEDRIVE`, `HOMEPATH`, `HOME`, `APPDATA` and `LOCALAPPDATA` too.
2. *The `outdated` table broke its columns.* Providers were printed verbatim:
   rust puts the commit and date inside its version field, so `Latest` read
   `1.99.1 (58cf8f9c3 2026-10-13)`, and PowerShell titles releases with prose,
   giving `v7.6.7 Release of PowerShell`. `versions.ExtractVersion` pulls out the
   token. It deliberately keeps an attached pre-release suffix (`3.15.0a1`,
   `1.24rc1`) — dropping it makes an alpha read as a final release.
3. *Windows Defender quarantined the executable.* It flagged the freshly built
   binary as `Trojan:Win32/Bearfoos.A!ml`, an ML false positive, and deleted it
   from disk seconds after it first ran. A 10-line Go program in the same folder
   was not flagged, so it was not generic to Go; the binary had an empty resource
   directory — no version info, no manifest. Adding both via a committed `.syso`
   fixes it, measured with Defender's own scanner:

   ```
   MpCmdRun.exe -Scan -ScanType 3 -File <path>
   without resources: found 1 threats  → cleaned
   with resources:    found no threats
   ```

**`cdecc1e`** — documents that false positive in the README troubleshooting
section.

---

## 10. Known gaps and unverified areas

Listed deliberately. These are where a reviewer's attention is most valuable.

- **No git remote.** `install.sh` and `install.ps1` fetch from
  `raw.githubusercontent.com/zedrun/zedrun-go/…` and the release assets. Until the
  repository is published those URLs 404. The installers are tested against a
  `file://` stub, not against the real URLs.
- **`install.ps1` is only syntax-checked.** `TestPowerShellInstallerParses` runs it
  through the PowerShell parser to prove it is valid PowerShell. It has never been
  executed end-to-end, so its download/verify/replace logic is unproven — unlike
  `install.sh`, which has four tests including a deliberate rejection of a
  non-running binary. **This is the largest untested surface in the repository.**
- **The binary is unsigned.** The Windows resources stop Defender's current ML
  heuristic; they are not a signature, and a future heuristic change or a
  different AV product could still flag it. Code signing is the real fix and needs
  a certificate the project does not have.
- **Python parity is believed complete but was not exhaustively diffed.** The Go
  version matches the `.zedrun.json` format and the managed-block format, and the
  command surface was compared against the original. A reviewer with the Python
  source at `/home/zane/zedrun` should treat "flag-by-flag parity" as a claim to
  check rather than an established fact.
- **`internal/jsonc` is the highest-risk code in the repository.** It rewrites a
  file the user cares about. It is comment-preserving and touches only the managed
  region, and it is tested — but a bug here costs the user their editor config,
  and the tests are not exhaustive about pathological inputs (a managed block with
  a trailing comment and no comma was one real bug, now fixed).
- **The TUI has no file picker.** `r` runs a file from `--file`, `$ZED_FILE`, or a
  typed path. There is no way to browse for one.
- **`internal/version` has no tests** (a single constant).
- **The CI matrix has never run.** There is no remote, so `.github/workflows/`
  is unexercised. The Go floor was verified locally by building on exactly
  `go1.24.0` under `GOTOOLCHAIN=local`, not by CI.

---

## 11. What I would most like checked

In rough order of value:

1. **`internal/jsonc`** — the managed-block editing. Does it corrupt the user's
   Zed config under inputs the tests do not cover?
2. **`internal/detect` and `internal/platform`** — the candidate ordering and the
   `PATH` handling, now that the Windows environment lookup has been rewritten.
   Is `platform.Env` right to stay case-sensitive on Unix, or does that break
   anything that relied on folding?
3. **`internal/versions`** — `ExtractVersion`, `Judge` and the comparison rules.
   The suffix rule is the newest and least exercised logic in the package; is
   there a real version string it mangles?
4. **The `tea.Cmd` concurrency** — `u`, `d` and the startup scan now run in
   separate goroutines sharing a `ProbeCache`. The mutex is there and `-race` is
   clean, but the interaction is new.
5. **`internal/cli`** — the argument parser and the exit-code table. Are there
   inputs that produce the wrong code, or a flag combination that misbehaves?
6. **Whether the spec in `docs/superpowers/specs/` is actually satisfied** —
   anything planned and not delivered is more interesting than anything delivered
   differently.

---

## 12. Where to look

```
docs/superpowers/specs/2026-10-19-zedrun-go-design.md   the design spec
docs/superpowers/plans/2026-10-19-zedrun-go.md           the 15-task plan
docs/BUG-REPORT.md                                       the dashboard defects, with a repro harness
docs/SETUP.md                                            per-OS setup guide
README.md                                                user-facing reference
internal/tui/bugrepro_test.go                            the evidence harness for the dashboard bugs
internal/tui/race_test.go                                the concurrency regression guard
internal/dist/install_test.go                            the installer tests
```

Reproduce the headline claims:

```bash
export PATH="$HOME/.local/go/bin:$PATH" && cd /home/zane/zedrun-go
go test -count=1 ./... && go test -race -count=1 ./...   # 15/15, race clean
gofmt -l . ; go vet ./...                                # both silent
make dist && file dist/zedrun-go-linux-amd64             # statically linked
go test ./internal/tui/ -run TestBug -v                  # the dashboard evidence
```
