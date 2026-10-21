# Toolchain-Assist Revamp — Design

> **For agentic workers:** The executor on the user's machine is a free web Claude account. It cannot run commands on the machine directly. It drives the user: reads the diagnosis output, then issues exact copy-paste instructions and verifies by reading the user's response back.

## Intent and the why
zedrun-go was meant to make a language-toolchain setup a one-step
`zedrun-go get <tool> --run --yes`. On a clean Windows / college-PC
machine that promise breaks at the FIRST step: getting the *installer* to
run — PATH, shell profile, elevation, manager not installed, wrong shell.
The user's real workflow is: run `zedrun-go get` → hit a "not working"
wall → manually diagnose → fix PATH/shell → run again → hope it is still
wrong → repeat across free accounts. This revamp makes the toolchain
assist a first-class part of zedrun-go so the *local* run is
production-grade, and so a web-free full-featured Claude account can
complete the remaining setup on the user's machine (without a shell) by
driving the user through an exact, resumeable procedure.

## Goals
- Production-grade toolchain provisioning: correct routes per OS, correct
  elevation, progress-bar downloads, version-proven packages.
- Toolchain repair that is deterministic: on any shell (PowerShell,
  pwsh, cmd.exe, bash/zsh, WSL), on any PATH state, the assistant can
  diagnose and fix or hand back exact instructions.
- Web-free full-featured Claude operable: a second free account can resume
  where the first left off, with no re-explanation, by reading a state
  file and the previous session's transcript.

## Non-goals
- Undergoing tooling the toolchain-manager can use; installed programs
  remain outside zedrun-go's control.
- Rewriting the TUI from scratch; only extend the dashboard slices that
  need the new subsystem.
- Replacing package managers; zedrun-go remains a thin layer on top of
  winget/apk/apt/snap/npm/pip/cargo/rustup/nix.

## Anti-goals
- Automatically accepting a password prompt on the user's machine.
- Guessing a toolchain version the user wants; always say "the package for
  <tool id> version X now installs. Do you want me to continue?"
- Proceeding when a shell command needs a password or a user choice
  (the assistant must stop and ask).

## Constraints
- Go 1.24.0 floor (go.mod); builds stay static binaries (`CGO_ENABLED=0`),
  matching `make dist` (linux+darwin+windows × amd64+arm64).
- No shell-string evaluation: toolchain install commands are passed as
  argv, never through a shell, so spaces and semicolons in package names
  are safe (proven in `internal/provision`).
- Elevation is resolved by `provision.Elevate`, reused by the new subnet —
  the tool never prompts for a password, and never runs elevated on its own.
- The toolchain-assist subnet reads the user config and project
  `.zedrun.json` per the existing `config` package contract.
- A free full-featured Claude (via API/web) can only guide the user
  through an exact copy-paste protocol — it must not claim to have
  installed anything on the user's machine.

## The parts of how (the design decisions made)
1. New `internal/toolchain` package:
   - `Status` — one call that runs a full diagnosis: tool present? version
     ok? route available? manager present? elevation available?
   - `Repair` — on a failing property, returns the exact shell- and
     shell-profile-agnostic fix + the verify command.
   - `Resume` — a per-project state file that records diagnosis → last
     action → next step, so a new account can continue.
2. `internal/cli` adds `toolchain` subcommands: `status`, `diagnose`,
   `repair`, `verify`, `how-to`. Thin wrappers over the new package (no
   shell strings inside).
3. TUI dashboard slice: a new `unhealthy toolchain` row set, wired to
   `doctor`-compatible output.
4. Shell-agnostic instruction surface: every fix the assistant gives is
   one of three shapes: (a) `zedrun-go toolchain repair <tool>` (the tool
   does it); (b) `set-zedrun-path <dir>` (prints a platform-correct,
   shell-profile-agnostic invocation to append to the user's profile —
   the tool never writes to it); (c) copy-paste block containing the full
   command as plain text.
5. Resume protocol: a JSON state file at
   `$XDG_STATE_HOME/zedrun-go/toolchain/<project>.json` (or
   `~/.local/state/zedrun-go/toolchain/<project>.json` on Linux,
   `%LOCALAPPDATA%\zedrun-go\toolchain\<project>.json` on Windows)
   recording machine, shell, toolchain set, subjects, last submission,
   pending, state. On a new session the assistant reads it and continues,
   without re-asking.

## What's left to the builder
- New package `internal/toolchain`, tests for detection/repair.
- CLI subcommands wiring + TUI slice.
- install.sh / install.ps1 updated to lay down the subsystem + resume
  state.
- Progress-bar downloads for direct (URL-only) toolchains already exist
  in `internal/provision`; reuse them in the `toolchain repair` path
  where there is no manager argv.
- A `how-to` renderer that outputs copy-paste-safe blocks the user can
  run, and a `--json` mode the assistant can parse.

## Stack
- Go 1.24 (floor from go.mod); stdlib only for the new package; charmbracelet/bubbletea for the existing TUI.
