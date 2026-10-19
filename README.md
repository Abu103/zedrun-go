# zedrun-go

**Run the file you're editing, in any language, without leaving Zed.**

One key press compiles and runs the current file. zedrun-go finds every compiler
and interpreter already on your machine, picks the right one, shows you what it
picked and where it lives, and tells you when something is out of date — stable
or pre-release.

- **One static binary.** No Python, no Node, no runtime to install first.
- **Nothing to configure.** It finds your toolchains; you don't tell it where they are.
- **Your build output never lands in your project.** Artifacts go to a cache directory.
- **No telemetry.** The network is used only by `outdated` and `doctor --sources`.

[![CI](https://github.com/zedrun/zedrun-go/actions/workflows/ci.yml/badge.svg)](https://github.com/zedrun/zedrun-go/actions/workflows/ci.yml)
[![Release](https://github.com/zedrun/zedrun-go/actions/workflows/release.yml/badge.svg)](https://github.com/zedrun/zedrun-go/actions/workflows/release.yml)
[![Licence: MIT](https://img.shields.io/badge/licence-MIT-blue.svg)](LICENSE)

This is the Go rewrite of [zedrun](../zedrun) and reads the same `.zedrun.json`
config, so an existing setup keeps working.

---

## Install

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.ps1 | iex
```

**macOS / Linux:**

```sh
curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh
```

The script detects your OS and architecture, downloads the matching release,
checks that it actually runs, and puts it on your `PATH`. To remove it again:

```sh
curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh -s -- --uninstall
```

**From source** — Go 1.24 or newer and nothing else:

```sh
git clone https://github.com/zedrun/zedrun-go
cd zedrun-go
make build          # -> ./zedrun-go
```

Then wire it into Zed:

```sh
zedrun-go install
```

Not sure which route suits your machine? **[docs/SETUP.md](docs/SETUP.md)** has a
step-by-step guide for Linux, macOS, Windows and WSL — including how to set it
up with no Go and no network, and how to publish your own release.

---

## Sixty-second tour

Start by seeing what it found:

```console
$ zedrun-go doctor
System
  os          linux
  go          go1.24.0
  config_dir  /home/you/.config/zedrun
  cache_dir   /home/you/.cache/zedrun

Tools
Tool    Version  Origin  Path
-----------------------------------------------------
gcc     13.3.0   system  /usr/bin/gcc
go      1.24.0   system  /home/you/.local/go/bin/go
node    24.22.0  system  /usr/bin/node
python  3.12.3   system  /usr/bin/python3

Languages (default tool)
Language    Default
---------------------
c           gcc
cpp         g++
go          go
javascript  node
python      python
...

No problems detected.
```

Run something:

```console
$ zedrun-go run hello.py -- --verbose
python 3.12.3 (system) /usr/bin/python3
hello from python
argv: ['--verbose']
[Done] exit code 0 in 0.06s
```

The first line is the toolchain it chose and where it is, so a surprising result
is never a mystery. Ask specifically if you want to know why:

```console
$ zedrun-go which main.c
main.c: c
  tool     gcc
  version  13.3.0
  origin   system
  path     /usr/bin/gcc
```

Check what's out of date:

```console
$ zedrun-go outdated
Tool     Installed    Latest       Status      Bump
----------------------------------------------------
gcc      13.3.0       16.2.0       outdated    major
go       1.24.0       go1.24.3     outdated    patch
node     24.22.0      v24.22.0     up-to-date
python   3.12.3       3.13.7       outdated    minor
```

Then open the dashboard — `zedrun-go` with no arguments:

| key | action |
|-----|--------|
| `↑` `↓` / `k` `j` | move between languages |
| `PgUp` `PgDn` / `Ctrl+U` `Ctrl+D` | scroll the view |
| `Home` `End` / `g` `G` | jump to the top / bottom |
| `r` | run a file — see below for where the path comes from |
| `d` | doctor |
| `u` | check installed versions against the latest (`p` picks the channel) |
| `t` | pick a toolchain for the highlighted language |
| `i` | preview the Zed install; press `i` again to apply |
| `m` | turn mouse capture on (the wheel scrolls) or off |
| `c` | clear the output pane |
| `p` | toggle pre-release for the update check |
| `q` / `Esc` / `Ctrl+C` | quit |

The view is windowed to your terminal and the footer always stays on the last
row, so the key hints never scroll away. Long output **wraps** rather than
truncating, so the actionable end of a line — a flag, a path — stays readable.

`d` and `u` run in the background: both do real work (directory walks, network
round trips) and the dashboard stays responsive while they finish.

### Mouse capture is off by default

Mouse capture stops the terminal from selecting text, and reading output you
may want to copy is the point of this dashboard. So it starts off, and the
wheel is handled by your terminal as arrow keys — which move the language
cursor and scroll to follow it. Press `m` when you want the wheel to scroll the
pane directly; press it again to get text selection back.

### Where `r` gets its file

In order:

1. `--file PATH` — `zedrun-go --file main.py`
2. `$ZED_FILE` — Zed exports this when the dashboard runs as one of its tasks
3. otherwise the dashboard asks, and you type a path

```sh
zedrun-go --file src/main.py    # dashboard opens knowing what to run
```

Piped or non-interactive output falls back to plain text, so the same binary
works in scripts and CI.

---

## In Zed

`zedrun-go install` adds a managed block to Zed's `tasks.json` and
`keymap.json`. The edit is **text insertion**, so your own tasks, keybindings
and comments are left exactly as they were. It backs the files up once, and it
will not rebind a key you already use.

| key | task |
|-----|------|
| `F6` | Run File |
| `Shift+F6` | Run File (release) |
| `F7` | Pick Toolchain |
| `Shift+F7` | Doctor |
| `Ctrl+F7` | Check Updates |

There is also a **Check Updates (pre-release)** task in the task list, without a
default key. Bind it yourself if you want it.

The bindings are scoped to Zed's `Editor` context, so they win while you are
editing without stealing the debugger's keys.

> **The tasks record the absolute path of the binary.** If you move
> `zedrun-go`, run `zedrun-go install` again so the tasks point at the new
> location. `zedrun-go install --dry-run` shows the change before it happens.

To undo everything: `zedrun-go uninstall`. Add `--purge` to delete the cache and
data directories too.

---

## Command reference

Run `zedrun-go <command> --help` for any of these.

| command | what it does |
|---------|--------------|
| `run FILE` | compile if needed, then run, returning your program's exit code |
| `which FILE` | the toolchain that would run FILE, and why |
| `doctor` | system, toolchains, defaults, and problems |
| `outdated` | installed vs latest upstream |
| `use` | set a language preference |
| `pick` | list the toolchains that can run a language |
| `install` | add the Zed tasks and keybindings |
| `uninstall` | remove them |
| `languages` | list supported languages and extensions |
| `clean` | delete the cache |

### `run`

```
zedrun-go run FILE [args…] [--release] [--tool ID] [--timeout D]
                   [--build-dir DIR] [--dry-run] [-v] [-- args…]
```

| flag | meaning |
|------|---------|
| `--release` | add the toolchain's release flags (`-O2`, `--release`, …) |
| `--tool ID` | force one toolchain, e.g. `--tool clang` |
| `--timeout D` | kill the run after `D` (`30`, `500ms`, `2m`); `0` disables |
| `--build-dir DIR` | write build artifacts to `DIR` instead of the cache |
| `--dry-run` | print the commands without running them |
| `-v`, `--verbose` | echo each command and the environment it ran in |
| `--` | everything after this goes to your program |

```sh
zedrun-go run main.c
zedrun-go run main.py -- --verbose input.txt
zedrun-go run main.go --release
zedrun-go run main.c --dry-run          # see the compiler command first
```

### `doctor`

| flag | meaning |
|------|---------|
| `--json` | machine-readable output |
| `--all` | also list unusable installs and every problem |
| `--sources` | check that each version source is reachable (network) |

### `outdated`

| flag | meaning |
|------|---------|
| `--json` | machine-readable output |
| `--pre` | compare against pre-releases too (beta, rc, EA) |
| `--fail-on-outdated` | exit `1` when anything is out of date — for CI |

```sh
zedrun-go outdated --fail-on-outdated   # in a CI step
```

Results are cached, so a repeat run works offline.

### `use` and `pick`

```sh
zedrun-go pick python
```

```console
python — installed toolchains:
  * python     3.12.3     ok           /usr/bin/python3
  * python     3.11.9     ok           /usr/local/bin/python3

pin one with: zedrun-go use python <tool> --path <path>
```

When two installs share a tool id, `use <language> <tool>` cannot tell them
apart — the preference is keyed by tool id, which is the thing that collides.
Use `--path`:

```sh
zedrun-go use c clang                              # prefer clang for C
zedrun-go use python python --path /usr/bin/python3 # pin an exact executable
```

`--config FILE` writes to a specific config file instead of the project one.

---

## Configuration

A project `.zedrun.json`, found by walking upwards from the file you are
running, overrides the user config at:

| platform | user config |
|----------|-------------|
| Linux | `~/.config/zedrun/.zedrun.json` |
| macOS | `~/Library/Application Support/zedrun/.zedrun.json` |
| Windows | `%APPDATA%\zedrun\.zedrun.json` |

Comments and trailing commas are allowed.

```jsonc
{
  // try clang before gcc for C
  "prefer": { "c": "clang" },
  // pin an exact executable, or a directory to search
  "paths": { "gcc": "C:\\msys64\\ucrt64\\bin\\gcc.exe" },
  "extraDirs": ["D:\\tools\\bin"],
  "flags": { "c": ["-Wall", "-Wextra"] },
  "std": { "cpp": "-std=c++20" },
  "args": ["--verbose"],
  "timeout": 30,
  "showFooter": true
}
```

| key | meaning |
|-----|---------|
| `prefer` | language → tool id to try first |
| `paths` | tool id → exact executable (or a directory to search) |
| `extraDirs` | extra directories to search |
| `flags` | language → extra compiler/interpreter flags |
| `std` | language → `{std}` value |
| `args` | extra arguments passed to your program |
| `env` | environment overrides for the run |
| `languages` | custom or overridden language specs |
| `rules` | glob → command template, overriding the language |
| `clearScreen` | clear the terminal before each run |
| `showFooter` | show the `[Done] exit code …` line |
| `echoCommands` | print each command as it runs |
| `timeout` | seconds; `0` means no timeout |
| `sources` | override version-source URLs for `outdated` |

A corrupt or mistyped config **warns and falls back to defaults** — it never
blocks a run.

---

## How it works

**Detection.** For each tool, candidates are tried in order: a pinned path,
tool-specific directories, every `PATH` entry, well-known install locations
(MSYS2, LLVM, Homebrew, snap, pyenv, nvm, …), then your `extraDirs`. Duplicates
collapse, and WSL/WindowsApps shims sort last so they never shadow a real
toolchain. A candidate counts as usable only if running it with its version
arguments exits 0 in time, which rejects store stubs and broken installs.
Results are cached by path, mtime and size.

**Running.** The toolchain's own directory goes first on `PATH`, so a MinGW
build finds its runtime DLLs. Build output goes to the cache directory, never
into your project, and a failed compile stops the pipeline so a stale binary is
never run. The footer reports the exit code and the time, and explains NTSTATUS
codes such as `0xC0000135` (missing DLL).

**Exit codes.**

| code | meaning |
|------|---------|
| your program's code | on a run |
| `124` | the run was killed by `--timeout` |
| `127` | no toolchain found (with an install hint for your OS) |
| `1` | bad file, or `--fail-on-outdated` found something |
| `2` | usage error |
| `130` | interrupted with Ctrl-C |

**Network.** Only `outdated` and `doctor --sources` make requests, and the
results are cached so a later run works offline. Nothing else leaves your
machine.

---

## Troubleshooting

**`no toolchain found for x` (exit 127).** Nothing on your machine can run that
language. `zedrun-go pick <language>` lists what it looked for and prints an
install hint for your OS. If you have a toolchain somewhere unusual, add the
directory to `extraDirs` or pin it with `paths`.

**It picked a toolchain I didn't expect.** `zedrun-go which FILE` shows the
choice and the alternatives. Pin the one you want:

```sh
zedrun-go use c clang
```

**Two versions are installed and I want the other one.** `zedrun-go pick
<language>` lists them all with paths. Then pin by path — `use <lang> <tool>`
cannot distinguish them:

```sh
zedrun-go use python python --path /usr/local/bin/python3
```

**A tool shows as found but "not usable".** Probing it with its version
arguments did not exit 0 in time. Usually a package-manager shim or a
half-installed toolchain. `zedrun-go doctor --all` shows the note.

**`outdated` reports something strange.** Run `zedrun-go doctor --sources` to
see which upstream each product was read from and whether it was reachable. If
a source has moved, override it under `sources` in your config. Clear stale
results with `zedrun-go clean`.

**A run hangs.** Give it a timeout: `zedrun-go run main.py --timeout 10`, or set
`"timeout": 10` in your config.

**Zed says the task failed immediately.** The tasks record the absolute path of
the binary at install time. If you moved it, re-run `zedrun-go install`.

**Ctrl-C leaves a stray process.** Ctrl-C is forwarded to the child; if a
wrapper swallows it, use `--timeout`.

---

## Languages

C, C++, C#, Dart, Elixir, Go, Haskell, Java, JavaScript, Julia, Kotlin, Lua,
Perl, PHP, PowerShell, Python, R, Ruby, Rust, Scala, shell, SQL, Swift,
TypeScript and Zig.

```sh
zedrun-go languages        # the full list with extensions
```

Anything else can be added under `languages` in your config.

---

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make build    # build ./zedrun-go
make dist     # cross-compile the release assets into dist/
```

Releases are built with `CGO_ENABLED=0` for `linux`, `darwin` and `windows` on
`amd64` and `arm64`.

The design lives in [`docs/superpowers/specs/`](docs/superpowers/specs) and the
plan in [`docs/superpowers/plans/`](docs/superpowers/plans).

---

## Licence

MIT — see [LICENSE](LICENSE).
