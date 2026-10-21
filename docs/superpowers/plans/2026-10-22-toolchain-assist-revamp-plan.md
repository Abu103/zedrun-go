# Toolchain-Assist Revamp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a production-grade toolchain-assist subsystem to zedrun-go: a `toolchain` CLI family (status/diagnose/repair/verify/how-to), a TUI dashboard slice, a shell-agnostic instruction surface, and a per-project resume state file — so the tool can fix or instruct toolchain problems on Windows (PowerShell/pwsh/cmd) and Linux/macOS/WSL, and so a sandboxed free Claude account can finish the setup on another machine without re-explaining.

**Architecture:** A new `internal/toolchain` package holds real-time detection + guided repair + resume state. `internal/cli` adds a `toolchain` subcommand family that delegates to it (no shell strings inside). `internal/tui` adds a dashboard slice driven by `doctor`-compatible output. install.sh/install.ps1 lay down the subsystem + resume state.

**Tech Stack:** Go 1.24 (floor from go.mod); stdlib only for the new package; charmbracelet/bubbletea for the existing TUI.

## Global Constraints
- `go 1.24.0` floor from `go.mod`; `make dist` stays 6 static binaries (linux+darwin+windows × amd64+arm64, CGO_ENABLED=0). New code must not raise the floor or drop any target.
- No shell-string eval: toolchain install commands are passed as `exec` argv (proven in `internal/provision`), so spaces and semicolons in package names are safe.
- Elevation resolved by `provision.Elevate`, reused by the new subnet — the tool never prompts for a password, and never runs elevated on its own.
- The toolchain-assist subnet reads the user config and project `.zedrun.json` per the existing `config` package contract.
- A free full-featured Claude (API/web) is sandboxed; it cannot run commands on the user's machine. It drives the user through exact copy-paste instructions and verifies by reading outputs back. It must not claim to have installed anything on the user's machine.

## Review Focus
- **PATH order on Windows:** two installs of one tool id collide, so `zedrun-go use` pins by path; the repair path must surface the correct directory, not just the tool id. Add a test that a legacy bat/shim is shadowed by a real exe in an earlier PATH entry.
- **Elevation:** a method flagged `Elevated` must be explained (not run) on a shell without passwordless sudo, and via sudo only when `sudo -n` probes. Add a test where `sudo -n` fails and `Exec` returns the exact user command.
- **Shell persistence:** appending to `~/.bashrc` does not affect a `zsh` terminal. Add a test that the instruction renderer prints the shell profile (bash vs zsh) and the exact line to append.
- **Resume state:** a fresh account must be able to pick up a partial session from the state file; the resume JSON must survive a partial write (atomic write via temp+rename). Add a test that a half-written state file is ignored.
- **Download progress:** a direct-download route (no manager argv) streams with a progress bar and lands in `cache/downloads/<tool>`; the fix command must point at that exact path. Add a test that `execDownload` writes the file and prints the `Note`.

---

### Task 1: New package `internal/toolchain` — status + detect

**Files:**
- Create: `internal/toolchain/toolchain.go`
- Create: `internal/toolchain/detect.go`
- Create: `internal/toolchain/toolchain_test.go`

**Interfaces:**
- Consumes: `config` (Config), `detect` (ProbeCache), `platform` (OSName, CacheDir), `provision` (Method, LookPath, LookPath, RouteFinding)
- Produces: `toolchain.Status`, `toolchain.Repair`, `toolchain.Resume`

**Steps:**
- [ ] **Step 1: Write the failing test** for a `Status` struct that reports tool present/version/origin/route/manager-present/elevation-available.
- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/toolchain/...`.
- [ ] **Step 3: Implement `toolchain.go`** with `Status` struct + `Detect(ctx, cfg, cache, env, osName)` that fills it from `detect.FindInstalls` + `provision.LookPath`.
- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/toolchain/... -v`.
- [ ] **Step 5: Commit** `git add internal/toolchain/ internal/toolchain/_*.go && git commit -m "feat(toolchain): add Status detection"`.

### Task 2: Repair + verify (the "how to" renderer)

**Files:**
- Create: `internal/toolchain/repair.go`
- Create: `internal/toolchain/repair_test.go`

**Interfaces:**
- Consumes: `provision` (Method, LookPath, RunOptions), `platform`, `config`
- Produces: `Repair()` returns the one of the three instruction shapes (`(a)` tool repair, `(b)` path append, `(c)` copy-paste block).

**Steps:**
- [ ] **Step 1: Write the failing test** for the decision table rows: PATH missing, manager not installed, no sudo, elevation needed on Windows, stale PATH file.
- [ ] **Step 2: Run test to verify it fails.**
- [ ] **Step 3: Implement `repair.go`** — maps the diagnosis to the three instruction shapes.
- [ ] **Step 4: Run test to verify it passes.**
- [ ] **Step 5: Commit** `git add internal/toolchain/repair.go internal/toolchain/repair_test.go && git commit -m "feat(toolchain): add guided repair"`.

### Task 3: CLI `toolchain` subcommands

**Files:**
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Consumes: `toolchain` package
- Produces: `main zedrun-go toolchain status|diagnose|repair|verify|how-to`

**Steps:**
- [ ] **Step 1: Write the failing test** for `cmdToolchainStatus` / `cmdToolchainRepair` argument parsing.
- [ ] **Step 2: Run test to verify it fails.**
- [ ] **Step 3: Add `cmdToolchainX` cases to `MainWith`** plus the `commandHelp` entry and the `commandNames` slice.
- [ ] **Step 4: Run test to verify it passes.**
- [ ] **Step 5: Commit** `git add internal/cli/cli.go && git commit -m "feat(cli): add toolchain subcommand family"`.

### Task 4: Resume state file + atomic write

**Files:**
- Create: `internal/toolchain/resume.go`
- Create: `internal/toolchain/resume_test.go`

**Interfaces:**
- Consumes: `platform` (ConfigDir), `encoding/json`, `os`
- Produces: `Resume.Save()`, `Resume.Load()`, atomic write with temp+rename.

**Steps:**
- [ ] **Step 1: Write the failing test** for atomic write + partial-write rejection.
- [ ] **Step 2: Run test to verify it fails.**
- [ ] **Step 3: Implement `resume.go`** with `Save()` / `Load()` + `StatePath(env, osName, project)`.
- [ ] **Step 4: Run test to verify it passes.**
- [ ] **Step 5: Commit** `git add internal/toolchain/resume.go internal/toolchain/resume_test.go && git commit -m "feat(toolchain): add resume state file"`.

### Task 5: TUI dashboard slice

**Files:**
- Modify: `internal/tui/tui.go`
- Modify: `internal/tui/ui_test.go`

**Interfaces:**
- Consumes: `toolchain` (Status), `doctor` (same report shape as existing)
- Produces: `dashboardUnhealthyRow` banner + a `Get` key that opens the toolchain overlay.

**Steps:**
- [ ] **Step 1: Write the failing test** for the unhealthy-row view + `Get` key.
- [ ] **Step 2: Run test to verify it fails.**
- [ ] **Step 3: Implement the slice** — reuse the existing `doctor.Collect` shape; add a `toolchain` field to the model and a `g` key handler.
- [ ] **Step 4: Run test to verify it passes.**
- [ ] **Step 5: Commit** `git add internal/tui/tui.go internal/tui/ui_test.go && git commit -m "feat(tui): add toolchain dashboard slice"`.

### Task 6: install.sh / install.ps1 + resume state on install

**Files:**
- Modify: `install.sh`
- Modify: `install.ps1`

**Interfaces:**
- Consumes: `platform.ConfigDir`, `platform.ZedConfigDir`
- Produces: the `toolchain` package + resume state directory, then `zedrun-go install` lays down the executable.

**Steps:**
- [ ] **Step 1: Write the failing test** for a fresh install creating the resume state dir + the right `toolchain` package.
- [ ] **Step 2: Run test to verify it fails.**
- [ ] **Step 3: Implement** the two installers to bootstrap `internal/toolchain` + resume state.
- [ ] **Step 4: Run test to verify it passes.**
- [ ] **Step 5: Commit** `git add install.sh install.ps1 && git commit -m "feat(install): bootstrap toolchain assist state"`.

### Task 7: Verify, test, build, dist, release

**Files:** (same repo)
**Steps:**
- [ ] **Step 1:** `gofmt -l .` — expect clean.
- [ ] **Step 2:** `go build ./...` — expect rc 0.
- [ ] **Step 3:** `go vet ./...` — expect rc 0.
- [ ] **Step 4:** `go test -race -count=1 ./internal/toolchain/... ./internal/cli/... ./internal/provision/...` — expect green.
- [ ] **Step 5:** `go test -race -count=1 ./...` — expect 15/15 green.
- [ ] **Step 6:** `make dist` — expect 6 binaries.
- [ ] **Step 7:** `git add -A && git commit -m "feat(toolchain): production-grade toolchain assist"`.
- [ ] **Step 8:** Tag + push; release workflow builds all 6 binaries and attaches them + checksums.

---
