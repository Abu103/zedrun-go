---
name: toolchain-assist
description: Use when the user is trying to install or activate a language toolchain (gcc/clang/llvm, node, python, java, go, etc.) on a Windows or Linux machine and the shell, PATH, or manager says it is not working — or when the user is handing off the same diagnosis to a different free Claude account.
---

# Toolchain Assist

## Overview
A repeatable procedure an autonomous (web, free-account) Claude uses to
diagnose, repair, and re-provision a language toolchain on the user's
machine. It runs **without** a shell: the assistant cannot execute
commands itself, so it turns every finding into exact copy-paste blocks
the user runs, then re-reads and re-verifies.

This skill is the two-sided answer to "my PATH or shell is broken: now
what?" It covers the diagnosis table, the shell-agnostic command
reference, the repair decision table, the verify step, and the resume
protocol so a second free Claude account can continue where the first
stopped.

## When to Use
- The user runs `zedrun-go get <tool> --run --yes` (or a package-manager
  command) and anything after "detected" is wrong.
- `which <tool>` or `<tool> --version` is missing, stale, or points at the
  wrong installation.
- A package manager (winget/pip/brew/apt/snap/npm/rustup) is not found,
  or found but not active in the shell being used.
- PATH is set differently by Windows Terminal, PowerShell, and WSL.
- The user says "it worked earlier but now `<tool> --version` fails".
- A previous free Claude account abandoned the work mid-flow and a new one
  must continue.

## When NOT to Use
- The user has not yet generated a toolchain (nothing is broken).
- The failure is a zedrun-go bug (wrong install location, wrong version
  expectation), not a toolchain environment problem. Route to `zedrun-go doctor`.
- The failure needs interactive GUI login the user cannot provide.

## Core Loop
1. **Diagnose.** Run `zedrun-go doctor --json`, then the targeted probes
   below. Record results in a state file.
2. **Classify.** Match the symptoms to one row in the Decision Table.
3. **Fix.** Return ONE of three shapes:
   - `(a)` `zedrun-go toolchain repair <tool>` — the tool does the fix;
   - `(b)` `set-zedrun-path <dir>` — prints a platform-correct, shell-profile-agnostic
     invocation to append to the user's profile; the user runs it;
   - `(c)` copy-paste block with the exact command text.
4. **Verify.** Re-run the verified probes; confirm the assistant's claim
   with output, not with the user's "it looks fine".
5. **Resume record.** Append the action and result to the state file.

## Decision Table (the heart of the skill)
Each row maps symptoms to cause → fix → verify.

| Symptoms | Likely cause | Fix (pick one) | Verify |
|---|---|---|---|
| `<tool>` not found anywhere | PATH missing the directory, or the directory has never been added | `set-zedrun-path <dir>` (shape b) on every shell the user uses | probe the path; `<tool> --version` in a NEW terminal |
| `<tool>` found but wrong version | two installs, PATH order wrong, or pref was set wrong | `zedrun-go use <tool> <path>` or `zedrun-go pick` | `<tool> --version` + `zedrun-go which <file>` |
| manager not installed (winget/pip/brew/apt… is missing) | manager not installed | shape `a`: `zedrun-go toolchain repair <tool>`; interactive only, stop if a password is needed | `manager --version`; then `zedrun-go get <tool> --run --yes` |
| `sudo: command not found` | Linux without sudo, or WSL without a sudo shim | shape `b`: read `sudo -n true`; if it times out ask for ability to run as root; otherwise give apt/snap install steps | `sudo -n true` exits 0 |
| need Administrator on Windows | UAC | shape `a` with elevation explained; do not silently run elevated | run the exact command in an elevated terminal; `<tool> --version` |
| PATH changes do not last | shell profile not sourced, or the line was appended to the wrong file, or the directories were quoted wrong | shape `b`; print the exact line, and the file it goes into; tell the user to run it in the SAME shell they will start Zed in | `refreshed` terminal, then `<tool> --version` |
| downloader failed / not a zip | first-run PATH issue, or the download is corrupt | re-run the installer with `--yes`; verify checksum if the route provides one | `<tool> --version` |
| version mismatch against upstream | package manager lags upstream | shape `b`: read `zedrun-go outdated --json` and report the exact `update:` line the tool computed; do not promise upstream's number | `zedrun-go outdated` shows the fix command |
| free Claude account switched mid-task | no resume record | read the state file, continue, do not re-diagnose | see Resume below |

## Shell-Agnostic Command Reference
The assistant collects these into copy-paste blocks typed by the user.

### Windows — `cmd.exe`
| What | Command |
|---|---|
| print PATH | `echo %PATH%` |
| a specific path | `echo %PATH:%userprofile%=%userprofile%` (substitute `userprofile`) |
| set a var for one window | `set ZEDRUN_GO_TOOLS=C:\tools\llvm` |
| restart shell so PATH refreshes | already done by opening a new window |
| run as administrator (no password) | open the Start menu, type `cmd`, right-click *Command Prompt*, "Run as administrator", then paste the command |

### Windows — PowerShell / pwsh
| What | Command |
|---|---|
| print PATH | `echo $env:PATH` |
| set a var for one window | `$env:ZEDRUN_GO_TOOLS = "C:\tools\llvm"` |
| add a directory to PATH for this window | `$env:PATH = "C:\tools\llvm;" + $env:PATH` |
| check a tiny program runs | `Test-Path C:\tools\llvm\bin\clang.cmd` |
| open a NEW window (PATH refresh) | press `Win+R`, type `powershell` or `cmd`, press Enter |

### Windows — registry (the remote cause of most "works in one window, not another")
| What | Command |
|---|---|
| SDK install dirs | check `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\ProgramFilesUnder` and `HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\ProgramFiles` |
| where environment goes | `HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Control\Session Manager\Environment` (system-wide) and `HKCU\Environment` (user) |
| PowerShell module profile | `echo $PROFILE` (user) and `$PSHOME\Profile.ps1` (system) |

### Linux / macOS / WSL — bash / zsh
| What | Command |
|---|---|
| print PATH | `echo "$PATH"` |
| set a var for this window | `export ZEDRUN_GO_TOOLS=/opt/llvm/bin` |
| add a directory to PATH for this window | `export PATH="/opt/llvm/bin:$PATH"` |
| the file each shell sources | bash: `~/.bashrc` or `~/.bash_profile` or `~/.profile`; zsh: `~/.zshrc`; note there is no single startup file — check what each shell reads |
| reload after editing | `source ~/.bashrc` (or the file you edited) |

## Resume Protocol (handoff between free accounts)
A second free Claude account must be able to continue where the first
stopped, with NO re-explanation. It does this by reading a state file the
first account wrote.

**State file locations** (match the user's OS):
- Linux/macOS: `$XDG_STATE_HOME/zedrun-go/toolchain/<project>.json`
  (defaults to `~/.local/state/zedrun-go/toolchain/<project>.json`)
- Windows: `%LOCALAPPDATA%\zedrun-go\toolchain\<project>.json`
- `<project>` is the directory the user started from (e.g. `zedrun-go`).

**State JSON** (the assistant reads and appends only):
```json
{
  "machine": "hostname + user (e.g. `pc-alex\\alex`)",
  "shell": "powershell / pwsh / cmd.exe / bash / zsh / wsl",
  "toolchain": { "toolId": { "version": "...", "path": "...", "origin": "..." } },
  "subjects": [
    { "id": 1, "symptom": "...", "cause": "...", "fix": "...", "verify": "...", "status": "done" }
  ],
  "lastSubmission": 12,        // the user's message number this account answered
  "pending": [ "subject 3" ],
  "state": "diagnosing | repairing | verifying | done"
}
```

**Protocol steps for a new account:**
1. Read `state.json`.
2. If `pending` is non-empty, pick the first subject and re-verify the fix
   with a fresh probe before touching anything else.
3. Append the subject to `subjects[].status = "done"`, add a `lastAction` /
   `lastResult` pair, and enqueue the next pending subject.
4. If `pending` is empty and `state == "done"`, tell the user the setup is
   complete and show the `zedrun-go which <file>` check.
5. If the state file is missing or malformed, start over from Diagnosis.

## Keywords
`toolchain`, `install`, `PATH`, `winget`, `apt`, `snap`, `brew`, `npm`,
`pip`, `cargo`, `rustup`, `shell`, `profile`, `resume`, `handoff`,
`sandboxed`, `free account`, `PowerShell`, `pwsh`, `cmd`, `ps1`

## Common Mistakes
| What goes wrong | Fix |
|---|---|
| editing `%PATH%` in the GUI but still getting the old path | environment blocks are per-shell; add the line to the shell profile the user starts Zed in, and `source` it |
| "works in PowerShell, not cmd" | paths with spaces break cmd's `%VAR%` parsing; use the administrator-privileged installer instead, or a directory with no spaces |
| `conda` / `pyenv` wrappers return "command not found" in a new terminal | their shell hooks are not loaded; run the "base init" snippet, or use the full path to the venv |
| LLVM gave "not a valid Win32 application" | you built/downloaded for the wrong architecture; check `Get-ComputerInfo` 64-bit and that `clang` is not a stub |
| winget asks for a restart | flight the restart; the install is complete, the shell just needs to refresh |

## Real-World Impact
Users on shared college machines stop losing hours to "which PATH is
broken" debates. The same diagnosis can be handed to any free Claude
account on a second machine without re-explaining.
