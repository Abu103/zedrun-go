# Setting up zedrun-go

Three ways to get the binary. Pick one.

| | needs | gets you |
|---|---|---|
| [**1. Published release**](#1-published-release) | internet | the released binary, on your `PATH`, in one line |
| [**2. Build from source**](#2-build-from-source) | Go 1.24+ | a binary built from the exact commit you have |
| [**3. Copy a prebuilt binary**](#3-copy-a-prebuilt-binary) | nothing | a working binary with no Go and no network |

Option 1 only works once the repo is published — see
[Publishing your own release](#publishing-your-own-release). Until then use 2
or 3.

Then do [Wire it into Zed](#wire-it-into-zed) either way.

---

## 1. Published release

**Linux / macOS / WSL:**

```sh
curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh
```

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.ps1 | iex
```

The script works out your OS and architecture, downloads the matching binary,
**runs it once to check it actually works**, and only then moves it into place.
If the download is broken your system is left untouched.

| | installs to | override with |
|---|---|---|
| Linux / macOS | `~/.local/bin` | `ZEDRUN_HOME=/somewhere` |
| Windows | `%LOCALAPPDATA%\zedrun` | `$env:ZEDRUN_HOME` |

It tells you if the directory is not on your `PATH`. On Windows it adds the
directory to your user `PATH` for you — restart your shell to pick it up.

To remove it again:

```sh
curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh -s -- --uninstall
```

```powershell
$s = irm https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.ps1
& ([scriptblock]::Create($s)) -Uninstall
```

---

## 2. Build from source

Needs **Go 1.24 or newer** and nothing else. No C compiler, no Node, no Python.

```sh
git clone https://github.com/zedrun/zedrun-go
cd zedrun-go
make build          # -> ./zedrun-go
```

Then either run it from that directory, or put it on your `PATH`.

**Linux / macOS / WSL**

```sh
mkdir -p ~/.local/bin
install -m 755 zedrun-go ~/.local/bin/zedrun-go
```

(`install` will not create the directory for you, and `-D` is GNU-only — hence
the `mkdir -p`.)

**Windows** (PowerShell). `make` is not part of Windows, so build directly:

```powershell
go build -trimpath -o zedrun-go.exe ./cmd/zedrun

$dest = "$env:LOCALAPPDATA\zedrun"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Copy-Item zedrun-go.exe "$dest\zedrun-go.exe"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dest*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dest", 'User')
}
```

Restart your shell afterwards.

> **Cross-compiling.** `make dist` builds all six targets at once with
> `CGO_ENABLED=0`, so the binaries are static and need no runtime installed:
>
> ```
> dist/zedrun-go-linux-amd64        dist/zedrun-go-windows-amd64.exe
> dist/zedrun-go-linux-arm64        dist/zedrun-go-windows-arm64.exe
> dist/zedrun-go-darwin-amd64       dist/zedrun-go-darwin-arm64
> ```
>
> The `darwin-amd64` build is the one for Intel Macs; `darwin-arm64` is Apple
> Silicon. You can build any of them from any OS.

---

## 3. Copy a prebuilt binary

If you already have a `dist/` directory from `make dist` (or a release
download), just take the one file for your machine and put it somewhere on your
`PATH`.

| your machine | take this file |
|---|---|
| Linux x86-64 (most PCs, WSL) | `zedrun-go-linux-amd64` |
| Linux ARM64 (Raspberry Pi, ARM server) | `zedrun-go-linux-arm64` |
| macOS Apple Silicon (M1 and later) | `zedrun-go-darwin-arm64` |
| macOS Intel | `zedrun-go-darwin-amd64` |
| Windows x86-64 | `zedrun-go-windows-amd64.exe` |
| Windows on ARM | `zedrun-go-windows-arm64.exe` |

Not sure which you have:

```sh
uname -s -m          # Linux/Darwin + x86_64/arm64
```

```powershell
$env:PROCESSOR_ARCHITECTURE   # AMD64 or ARM64
```

You can also point the installer at a local directory instead of GitHub — it
accepts a `file://` URL, so the safety check still runs:

```sh
ZEDRUN_BASE_URL=file:///home/you/zedrun-go/dist ZEDRUN_HOME=~/.local/bin sh install.sh
```

Rename the file to `zedrun-go` (or `zedrun-go.exe`) and make it executable:

```sh
chmod +x zedrun-go
```

**macOS:** if you copied the binary out of a browser download rather than
building it, macOS may have quarantined it. Clear that once:

```sh
xattr -d com.apple.quarantine ./zedrun-go
```

**Windows:** if SmartScreen blocks it, unblock the file once:

```powershell
Unblock-File .\zedrun-go.exe
```

---

## Wire it into Zed

Whatever route you took, this is the same:

```sh
zedrun-go install
```

It adds a managed block to Zed's `tasks.json` and `keymap.json`, by text
insertion — your own tasks, keybindings and comments are left alone. It backs
the files up once and will not rebind a key you already use.

| OS | Zed config directory |
|---|---|
| Linux / WSL | `~/.config/zed` |
| macOS | `~/Library/Application Support/Zed` |
| Windows | `%APPDATA%\Zed` |

Preview first if you like, and target a non-default Zed install:

```sh
zedrun-go install --dry-run
zedrun-go install --zed-dir /path/to/zed-config
```

Then restart Zed. You get:

| key | task |
|-----|------|
| `F6` | Run File |
| `Shift+F6` | Run File (release) |
| `F7` | Pick Toolchain |
| `Shift+F7` | Doctor |
| `Ctrl+F7` | Check Updates |

> The tasks record the **absolute path** of the binary as it was when you ran
> `install`. If you move it, run `zedrun-go install` again.

---

## Check it worked

```console
$ zedrun-go --version
zedrun-go 0.1.0

$ zedrun-go doctor
System
  os          linux
  ...
No problems detected.

$ echo 'print("hello")' > hello.py
$ zedrun-go run hello.py
python 3.12.3 (system) /usr/bin/python3
hello
[Done] exit code 0 in 0.05s
```

If `zedrun-go` is not found, the install directory is not on your `PATH`.
`zedrun-go install` still works with an absolute path, but the Zed tasks need a
bare command, so fix `PATH` first.

---

## Publishing your own release

The one-line installers fetch from
`https://github.com/<owner>/<repo>/releases/latest/download`, so nothing works
until a release exists. If you forked this or are setting up your own copy:

**1. Push the repo.** The install scripts reference `main`, so that branch has
to exist:

```sh
git remote add origin git@github.com:<you>/zedrun-go.git
git push -u origin impl:main
```

**2. Check CI is green** on the Actions tab. It runs on Linux, macOS and
Windows, and has a job that builds on exactly the Go version `go.mod` asks for.

**3. Tag a release.** The tag is what triggers the build:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`release.yml` cross-compiles all six binaries with `CGO_ENABLED=0` and attaches
them to the GitHub release automatically. About a minute later the one-liners
above work.

**4. Update the URLs** in this file and in `README.md` if you changed the
owner/repo, and set `ZEDRUN_REPO` in the installer environment if you want to
point at a different repo without editing anything:

```sh
ZEDRUN_REPO=you/zedrun-go curl -fsSL .../install.sh | sh
```
