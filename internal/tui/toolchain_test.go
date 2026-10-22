package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// toolchainModel is a dashboard whose only visible tool is a fake go that sits
// in a directory that is not on PATH.
func toolchainModel(t *testing.T, onPath bool) Model {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("posix fake executable")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "sdk", "go", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho 'go version go1.99.1 linux/amd64'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "empty")
	if onPath {
		path = bin
	}
	cfg := config.Default()
	cfg.ExtraDirs = []string{bin}
	env := map[string]string{"HOME": home, "PATH": path}
	m := NewModel(rows(), cfg, Options{Env: env, OS: "linux", Cache: detect.NewProbeCache("")})
	m.w, m.h = 100, 30
	return m
}

func TestToolchainKeyScansInTheBackground(t *testing.T) {
	m := toolchainModel(t, false)
	mm, cmd := send(t, m, "T")
	if cmd == nil {
		t.Fatal("T returned no command: the scan would block Update")
	}
	if got := strings.Join(mm.Output, "\n"); strings.Contains(got, "unhealthy") {
		t.Fatalf("the report appeared inside Update, so it is not asynchronous:\n%s", got)
	}
	if !mm.toolchainRunning {
		t.Error("no in-flight flag, so a second T would start a second scan")
	}
	done, _ := mm.Update(cmd())
	dm := done.(Model)
	got := strings.Join(dm.Output, "\n")
	if !strings.Contains(got, "unhealthy") || !strings.Contains(got, "not-on-path") || !strings.Contains(got, "fix: export PATH=") {
		t.Fatalf("the unhealthy go and its fix never reached the pane:\n%s", got)
	}
	if dm.toolchainRunning {
		t.Error("the in-flight flag was never cleared")
	}
}

func TestToolchainKeyIgnoresASecondPressWhileRunning(t *testing.T) {
	m := toolchainModel(t, false)
	mm, _ := send(t, m, "T")
	again, cmd := send(t, mm, "T")
	if cmd != nil {
		t.Fatal("a second T started a second scan")
	}
	if got := strings.Join(again.Output, "\n"); !strings.Contains(got, "already checking") {
		t.Errorf("no explanation for the ignored key:\n%s", got)
	}
}

func TestToolchainKeyHealthyMachineSaysSo(t *testing.T) {
	m := toolchainModel(t, true)
	_, cmd := send(t, m, "T")
	msg, ok := cmd().(toolchainMsg)
	if !ok {
		t.Fatalf("T produced %T", cmd())
	}
	got := strings.Join(msg.lines, "\n")
	// The host may have its own toolchains in well-known directories, so the
	// claim to check is that go, the one tool this test controls, is healthy.
	if strings.Contains(got, "  go ") {
		t.Fatalf("a go that resolves from PATH was reported unhealthy:\n%s", got)
	}
	if len(msg.lines) == 0 {
		t.Fatal("an empty result reads as a bug")
	}
}

func TestFooterMentionsTheToolchainKey(t *testing.T) {
	if f := footerFor(120); !strings.Contains(f, "T health") {
		t.Fatalf("T is undiscoverable: %q", f)
	}
	// Every width still gets a complete hint, never a truncated one.
	for _, w := range []int{60, 80, 100, 120} {
		if f := footerFor(w); !strings.HasSuffix(f, "q quit") {
			t.Errorf("width %d: footer lost q quit: %q", w, f)
		}
	}
}

func TestToolchainKeyIsSwallowedByOverlays(t *testing.T) {
	m := toolchainModel(t, false)
	m.updMenu = &updMenu{}
	got, cmd := send(t, m, "T")
	if cmd != nil || got.LastAction == "toolchain" || got.toolchainRunning {
		t.Fatal("T leaked through an open overlay to the dashboard underneath")
	}
}
