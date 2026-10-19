package cli

// commandHelp is the per-command help text. `zedrun-go <cmd> --help` is the
// first thing anyone types, so every command answers it instead of replying
// "unknown flag".
var commandHelp = map[string]string{
	"run": `usage: zedrun-go run FILE [args…] [flags]

Compile FILE first if its language needs it, then run it, using the
toolchain zedrun-go picked for that language. Your program's exit code
comes back unchanged.

flags:
  --release         add the toolchain's release flags (-O2, --release, …)
  --tool ID         force one toolchain, e.g. --tool clang
  --timeout D       kill the run after D (30, 500ms, 2m); 0 disables it
  --build-dir DIR   write build artifacts to DIR instead of the cache
  --dry-run         print the commands without running them
  -v, --verbose     echo each command and the environment it ran in
  --                everything after this is passed to your program

examples:
  zedrun-go run main.c
  zedrun-go run main.py -- --verbose input.txt
  zedrun-go run main.go --release
`,
	"which": `usage: zedrun-go which FILE [flags]

Show the toolchain that would run FILE: its version, where it lives, and
which other installations were considered and why they lost.

flags:
  --tool ID    check one specific toolchain instead of the automatic choice

example:
  zedrun-go which main.rs
`,
	"doctor": `usage: zedrun-go doctor [flags]

Report the system, every toolchain found (version, origin, path), the
default tool for each language, and any problems worth fixing.

flags:
  --json       machine-readable output
  --all        also list unusable installs and every problem
  --sources    check that each version source is reachable (network)

examples:
  zedrun-go doctor
  zedrun-go doctor --json
`,
	"outdated": `usage: zedrun-go outdated [flags]

Compare every installed toolchain against the latest upstream release.
Results are cached, so a repeat run works offline.

flags:
  --json               machine-readable output
  --pre                compare against pre-releases too (beta, rc, EA)
  --fail-on-outdated   exit 1 when anything is out of date (for CI)

examples:
  zedrun-go outdated
  zedrun-go outdated --pre
  zedrun-go outdated --fail-on-outdated
`,
	"use": `usage: zedrun-go use LANGUAGE TOOL [--path PATH] [--config FILE]

Write a language-to-toolchain preference. Add --path to pin an exact
executable: that is the only way to choose between two installations
that share a tool id, because the preference itself is keyed by tool id.

flags:
  --path PATH      pin this exact executable
  --config FILE    write to FILE instead of the project config

examples:
  zedrun-go use c clang
  zedrun-go use python python --path /usr/bin/python3
`,
	"pick": `usage: zedrun-go pick LANGUAGE

List every installation of every toolchain that can run LANGUAGE, and say
how to choose between them.

example:
  zedrun-go pick python
`,
	"install": `usage: zedrun-go install [flags]

Add zedrun-go's tasks and keybindings to Zed. The edit is a managed block
inserted as text, so your own tasks, keybindings and comments survive. The
files are backed up once, and a key you already bound is never rebound.

flags:
  --dry-run         show what would change without writing anything
  --zed-dir DIR     Zed's config directory (default: auto-detected)

example:
  zedrun-go install --dry-run
`,
	"uninstall": `usage: zedrun-go uninstall [flags]

Remove exactly what install added, leaving the rest of the file alone.

flags:
  --purge           also delete the cache and data directories
  --zed-dir DIR     Zed's config directory (default: auto-detected)

example:
  zedrun-go uninstall
`,
	"languages": `usage: zedrun-go languages

List every supported language and its file extensions.
`,
	"clean": `usage: zedrun-go clean

Delete the zedrun-go cache: probe results and build artifacts. The next
run rebuilds whatever it needs.
`,
}

// wantsHelp reports whether the arguments ask for help. It stops at "--", so
// `zedrun-go run main.py -- --help` still passes --help to your program.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}
