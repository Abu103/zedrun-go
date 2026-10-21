# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Dashboard
- Status bar under the header: the toolchain the cursor is on and how many tools are outdated (`U updates` when an update route exists). Reset when an update runs, so it never shows a stale count.
- The update overlay (`U`) is drawn inside the dashboard frame; header, status bar and footer stay on screen. Each tool is a block of name, versions and command, wrapped rather than truncated, with the manager's own version and upstream's both shown when they differ.
- Outdated rows in the output pane are bold amber, and the update hint beneath is coloured, so the tool that needs attention stands out.
- The footer now lists `i` (Zed setup) and, when the terminal is wide enough, `c` (clear); it picks the longest hint that fits instead of truncating.
- Mouse wheel no longer scrolls the hidden body while an overlay is open.

### Added

- A progress bar while downloading: `get TOOL --run --yes` on a URL-only (direct) route fetches the file itself and shows a `[████░░] 100%  1.0MB / 1.0MB` bar that updates as bytes arrive, ending on its own line. When no package manager can run the tool, this is the route that lands the file.
- `U` in the dashboard runs the update check and offers each fix; `enter` runs it
  through the same engine as `get --update`.
- Version feeds for bash (GNU FTP), ruby, php, perl (metacpan), sqlite3, R, GHC,
  Swift, Lua and TypeScript — each read from the project's own release source.
- A candidate probe for `apt`, so the update check asks the distro repository
  what it can actually install instead of assuming the vendor's version.

### Fixed

- The update check offered commands it could not honour. A manager may now
  update a tool only when it is the confirmed owner — the install origin names
  it, or winget's own list shows the package — so a node installed by the
  official installer is no longer offered `choco upgrade -y nodejs-lts`, a
  package choco does not have either.
- A manager whose index lags its vendor no longer has the lag papered over:
  winget's Go manifest sits at 1.27.0 while go.dev publishes 1.27.3, and the
  report says so instead of promising 1.27.3. Refreshing the winget source
  index cannot fix that, because winget-pkgs has not published the manifest.
- The version the command will install was gated on the installed version
  matching it, which is false precisely when there is an update to report, so
  the arrow never appeared. The gate is now "a plausible next version": newer,
  and the same product line.
- A version from a different product was ranked against the tool's own:
  Git.Git's 2.55.0.5 is Git's version, not bash's next one, and is now named as
  such rather than compared.
- Four bash installs printed four identical rows; a tool is one row.
- The update check's background command built its message from a copy of the
  model, so the outdated-tool list never reached the overlay.

- Initial Go rewrite of the Python `zedrun`: one static binary, no runtime
  dependencies.
- Toolchain detection with candidate ordering, real-path dedupe, WSL/WindowsApps
  deprioritisation, and a probe cache keyed by path, mtime and size.
- Runners for 25 languages, resolved from command templates with conditions
  (`project:`, `has:`, `version>=`, `os:`) and `--tool` / `prefer` overrides.
- `outdated` across 17 version providers (GitHub Atom with REST fallback, Go,
  Node, Rust, Zig, Adoptium, .NET, Python, GCC, Dart) with a stable/pre-release
  channel and a disk cache that serves stale data when offline.
- `doctor` report (system, tools, languages, problems) with `--json` and
  `--sources`.
- `install` / `uninstall` that edit Zed's `tasks.json` and `keymap.json` by text
  insertion inside a managed block, preserving comments and user entries.
- A bubbletea TUI dashboard, with a plain-text fallback when output is piped.
- One-line installers (`install.sh`, `install.ps1`) and a cross-compiling release
  workflow for linux/darwin/windows × amd64/arm64.
