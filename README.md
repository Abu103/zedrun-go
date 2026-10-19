# zedrun-go

A Code Runner for [Zed](https://zed.dev): one key runs the current file in any
language, it finds every compiler and interpreter already on your machine, it
tells you what is installed and where, and it checks whether any of them are out
of date — stable or pre-release. One static binary, no runtime dependencies.

This is the Go rewrite of [zedrun](../zedrun). It reads the same `.zedrun.json`.

## Install

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.ps1 | iex
```

macOS / Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh
```

Both scripts detect your OS and architecture, download the matching release
binary, check that it runs, and put it on your PATH. Add `--uninstall` to remove
it again.

Or build it yourself — Go 1.22+ and nothing else:

```sh
git clone https://github.com/zedrun/zedrun-go
cd zedrun-go
make build      # -> ./zedrun-go
```

## Use

```sh
zedrun-go                       # open the dashboard (TUI)
zedrun-go run main.c            # run a file
zedrun-go run main.c --release  # ...with the release flags
zedrun-go run main.py -- --verbose   # pass arguments to your program
zedrun-go which main.rs         # which toolchain would run it, and why
zedrun-go doctor                # what is installed, where, and any problems
zedrun-go outdated              # installed vs latest stable
zedrun-go outdated --pre        # ...including beta / rc / preview / EA
zedrun-go use c clang           # prefer clang over gcc for C
zedrun-go install               # add the Zed tasks and keybindings
zedrun-go uninstall             # remove them again
```

`zedrun-go` with no arguments opens the TUI:

| key | action |
|-----|--------|
| `r` | run a file |
| `d` | doctor |
| `u` | check for updates (`p` toggles pre-release) |
| `t` | pick a toolchain for the highlighted language |
| `i` | install the Zed tasks |
| `q` | quit |

Piped or non-interactive output falls back to plain text, so the same binary
works in scripts and CI.

## In Zed

`zedrun-go install` adds a managed block to Zed's `tasks.json` and `keymap.json`
— by text insertion, so your own entries and comments survive. It backs the
files up once, refuses to rebind a key you already use, and `uninstall` removes
exactly what it added.

| key | task |
|-----|------|
| `F6` | Run File |
| `Shift+F6` | Run File (release) |
| `F7` | Pick Toolchain |
| `Shift+F7` | Doctor |
| `Ctrl+F7` | Check Updates |

The bindings are scoped to Zed's `Editor` context, so they win while you are
editing without stealing the debugger's keys.

## Configuration

A project `.zedrun.json` (found upwards from the file) overrides the user config
in your config directory. Comments and trailing commas are allowed.

```jsonc
{
  // try clang before gcc for C
  "prefer": { "c": "clang" },
  // pin an exact executable
  "paths": { "gcc": "C:\\msys64\\ucrt64\\bin\\gcc.exe" },
  "extraDirs": ["D:\\tools\\bin"],
  "flags": { "c": ["-Wall"] },
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
| `flags` | language → extra flags |
| `std` | language → `{std}` value |
| `args` | extra arguments passed to your program |
| `env` | environment overrides for the run |
| `languages` | custom or overridden languages |
| `rules` | glob → command template, overriding the language |
| `clearScreen` / `showFooter` / `echoCommands` | display toggles |
| `timeout` | seconds; 0 means no timeout |

A corrupt or mistyped config warns and falls back to defaults — it never blocks
a run.

## How it behaves

- **Detection.** For each tool, candidates are tried in order: a pinned path,
  tool-specific directories, every `PATH` entry, well-known install locations
  (MSYS2, LLVM, Homebrew, snap, pyenv, nvm, …), then your `extraDirs`. Duplicates
  collapse, and WSL/WindowsApps shims sort last so they never shadow a real
  toolchain. A candidate counts as usable only if running it with its version
  arguments exits 0 in time — that rejects store stubs and broken installs.
  Results are cached by path, mtime and size.
- **Running.** The toolchain's own directory goes first on `PATH`, so a MinGW
  build finds its runtime DLLs. Build output goes to the cache directory, never
  into your project, and a failed compile stops the pipeline so a stale binary is
  never run. The footer reports the exit code and time, and explains NTSTATUS
  codes such as `0xC0000135` (missing DLL).
- **Exit codes.** Your program's code on a run; `127` when no toolchain is found
  (with an install hint for your OS); `1` for a bad file; `2` for a usage error;
  `130` on Ctrl-C.
- **No telemetry.** The only network access is `outdated` and
  `doctor --sources`, and results are cached so a later run works offline.

## Languages

C, C++, C#, Dart, Elixir, Go, Haskell, Java, JavaScript, Julia, Kotlin, Lua,
Perl, PHP, PowerShell, Python, R, Ruby, Rust, Scala, shell, SQL, Swift,
TypeScript and Zig — plus anything you add under `languages`.

```sh
zedrun-go languages        # the full list with extensions
```

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make build    # build ./zedrun-go
make dist     # cross-compile the release assets into dist/
```

The design lives in `docs/superpowers/specs/` and the plan in
`docs/superpowers/plans/`.

## Licence

MIT — see [LICENSE](LICENSE).
