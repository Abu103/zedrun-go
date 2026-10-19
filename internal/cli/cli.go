// Package cli parses arguments and dispatches zedrun-go commands.
package cli

import (
	"fmt"
	"io"

	"github.com/zedrun/zedrun-go/internal/version"
)

// usage is printed when no command is given or an unknown one is used.
const usage = `zedrun-go — Code Runner for Zed

usage: zedrun-go <command> [options]

commands:
  run        run a file
  which      show the toolchain a file would use
  doctor     diagnostics
  outdated   compare installed vs latest
  use        set a language preference
  pick       choose a toolchain for a language
  install    install into Zed
  uninstall  remove from Zed
  languages  list supported languages
  clean      clear the zedrun-go cache

Running with no command opens the TUI.
`

// Main runs zedrun-go with the given arguments and returns its exit code.
// It never calls os.Exit, so tests can drive it directly.
func Main(args []string, out, errOut io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version":
			fmt.Fprintf(out, "zedrun-go %s\n", version.Version)
			return 0
		case "-h", "--help", "help":
			fmt.Fprint(out, usage)
			return 0
		}
	}
	fmt.Fprint(errOut, usage)
	return 2
}
