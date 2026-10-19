// Command zedrun is the zedrun-go entry point: a Code Runner for Zed.
package main

import (
	"os"

	"github.com/zedrun/zedrun-go/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
