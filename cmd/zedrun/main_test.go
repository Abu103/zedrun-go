package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/cli"
	"github.com/zedrun/zedrun-go/internal/version"
)

func TestVersionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Main([]string{"--version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), version.Version) {
		t.Fatalf("output %q does not contain version %q", out.String(), version.Version)
	}
}
