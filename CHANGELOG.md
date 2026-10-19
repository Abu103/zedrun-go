# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
