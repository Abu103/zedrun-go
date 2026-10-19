package tui

import (
	"sync"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
	"github.com/zedrun/zedrun-go/internal/sources"
)

// The dashboard runs the toolchain scan and, once u landed, the update check in
// separate goroutines, and both walk the same ProbeCache. If that cache is not
// safe for concurrent use the race detector says so here.
//
//	go test ./internal/tui/ -race -run TestBackgroundWorkSharesOneCache
func TestBackgroundWorkSharesOneCache(t *testing.T) {
	opts := Options{Cache: detect.NewProbeCache(""), OS: "linux"}

	// the scan, exactly as Init launches it
	scan := NewModel(nil, config.Default(), opts).Init()
	if scan == nil {
		t.Fatal("Init returned no scan command")
	}

	// the update check, exactly as the u key launches it
	um := NewModel(rows(), config.Default(), opts)
	um.opts.Fetcher = sources.NewFetcher(t.TempDir()+"/sources.json", time.Minute,
		func(string) ([]byte, error) { return nil, nil })
	_, check := send(t, um, "u")
	if check == nil {
		t.Fatal("u returned no command")
	}

	// and the picker, which also probes on demand
	pick := NewModel(rows(), config.Default(), opts)

	var wg sync.WaitGroup
	for _, fn := range []func(){func() { scan() }, func() { check() }, func() { pick.choicesFor("python") }} {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); f() }(fn)
	}
	wg.Wait()
}
