package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/platform"
)

func env(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func fakeExe(t *testing.T, dir, name, script string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var myTool = catalog.Tool{ID: "mytool", Names: []string{"mytool"}, VersionArgs: []string{"--version"}}

func TestExtractVersionTable(t *testing.T) {
	cases := map[string]string{
		"gcc (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0": "13.3.0",
		"go version go1.23.4 linux/amd64":           "1.23.4",
		"v20.11.1":                                  "20.11.1",
		"Python 3.12.3":                             "3.12.3",
		"rustc 1.79.0 (129f3b996 2024-06-10)":       "1.79.0",
		"no version here":                           "",
	}
	for in, want := range cases {
		if got := ExtractVersion(in); got != want {
			t.Errorf("ExtractVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCandidateOrderAndDedupe(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	fakeExe(t, dirA, "mytool", "#!/bin/sh\necho 1.0.0\n")
	fakeExe(t, dirB, "mytool", "#!/bin/sh\necho 2.0.0\n")

	got := CandidatePaths(myTool, config.Default(), env("PATH", dirB+":"+dirA+":"+dirB), "linux")
	if len(got) != 2 {
		t.Fatalf("CandidatePaths = %v, want 2 unique entries", got)
	}
	if got[0] != filepath.Join(dirB, "mytool") || got[1] != filepath.Join(dirA, "mytool") {
		t.Fatalf("order = %v, want PATH order %s then %s", got, dirB, dirA)
	}
}

func TestPinnedPathFirst(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	pinned := fakeExe(t, dirA, "mytool", "#!/bin/sh\necho 1.0.0\n")
	fakeExe(t, dirB, "mytool", "#!/bin/sh\necho 2.0.0\n")

	cfg := config.Default()
	cfg.Paths["mytool"] = pinned
	got := CandidatePaths(myTool, cfg, env("PATH", dirB), "linux")
	if len(got) == 0 || got[0] != pinned {
		t.Fatalf("pinned path not first: %v", got)
	}
}

func TestProbeOK(t *testing.T) {
	p := fakeExe(t, t.TempDir(), "mytool", "#!/bin/sh\necho 'mytool 1.2.3'\n")
	inst := Probe(myTool, p, nil, 5*time.Second)
	if !inst.OK {
		t.Fatalf("Probe not OK: %+v", inst)
	}
	if inst.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", inst.Version)
	}
	if inst.Tool != "mytool" || inst.Path != p {
		t.Fatalf("Install = %+v", inst)
	}
}

func TestProbeNonzeroExitUnusable(t *testing.T) {
	p := fakeExe(t, t.TempDir(), "bad", "#!/bin/sh\nexit 3\n")
	if inst := Probe(myTool, p, nil, 5*time.Second); inst.OK {
		t.Fatalf("nonzero exit reported usable: %+v", inst)
	}
}

func TestProbeTimeoutUnusable(t *testing.T) {
	p := fakeExe(t, t.TempDir(), "slow", "#!/bin/sh\nsleep 5\n")
	start := time.Now()
	inst := Probe(myTool, p, nil, 300*time.Millisecond)
	if inst.OK {
		t.Fatalf("timed-out probe reported usable: %+v", inst)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Probe waited %v, want it to give up near the timeout", elapsed)
	}
	if inst.Note == "" {
		t.Fatal("timed-out probe has no note")
	}
}

func TestGccClangBannerNoted(t *testing.T) {
	p := fakeExe(t, t.TempDir(), "gcc", "#!/bin/sh\necho 'clang version 18.1.0'\n")
	inst := Probe(catalog.GetTool("gcc"), p, nil, 5*time.Second)
	if inst.Note != "is clang" {
		t.Fatalf("Note = %q, want \"is clang\"", inst.Note)
	}
}

func TestCandidatePathsWithSharedCacheMatchesUncached(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gcc"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := catalog.Tool{ID: "c", Names: []string{"gcc"}}
	cfg := config.Default()
	cfg.Paths["c"] = filepath.Join(bin, "gcc")
	e := env()
	e["PATH"] = filepath.Join(home, "empty")

	want := CandidatePaths(tool, cfg, e, "linux")
	got := CandidatePathsWith(tool, cfg, e, "linux", platform.NewDirCache())
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cached = %v, want the same as uncached %v", got, want)
	}
	if len(want) == 0 || !strings.HasSuffix(want[0], "bin/gcc") {
		t.Fatalf("the pinned path must come first, got %v", want)
	}
}

func TestProbeCacheAvoidsRelaunch(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	p := fakeExe(t, dir, "counted", "#!/bin/sh\necho x >> "+counter+"\necho '1.0.0'\n")
	cache := NewProbeCache(filepath.Join(dir, "cache.json"))

	Probe(myTool, p, cache, 5*time.Second)
	second := Probe(myTool, p, cache, 5*time.Second)
	if !second.OK || second.Version != "1.0.0" {
		t.Fatalf("cached probe = %+v", second)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Fatalf("executable ran %d times, want 1 (cache miss)", n)
	}
}

func TestCorruptProbeCacheIgnored(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(cachePath, []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := NewProbeCache(cachePath)
	if _, ok := cache.Get("/nonexistent", nil, 1, 1); ok {
		t.Fatal("corrupt cache produced a hit")
	}
}

func TestLazyValidationStopsAtFirstOK(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	fakeExe(t, dirA, "mytool", "#!/bin/sh\necho '1.0.0'\n")
	fakeExe(t, dirB, "mytool", "#!/bin/sh\necho '2.0.0'\n")
	e := env("PATH", dirA+":"+dirB)

	lazy := FindInstalls(myTool, config.Default(), nil, false, e, "linux")
	if len(lazy) != 1 || !lazy[0].OK {
		t.Fatalf("lazy = %+v, want exactly one usable install", lazy)
	}
	all := FindInstalls(myTool, config.Default(), nil, true, e, "linux")
	if len(all) != 2 {
		t.Fatalf("validateAll returned %d installs, want 2", len(all))
	}
}

func TestLowPriorityOriginsLast(t *testing.T) {
	base := t.TempDir()
	normal := filepath.Join(base, "normal")
	winApps := filepath.Join(base, "WindowsApps")
	fakeExe(t, normal, "mytool", "#!/bin/sh\necho '1.0.0'\n")
	fakeExe(t, winApps, "mytool", "#!/bin/sh\necho '2.0.0'\n")

	got := FindInstalls(myTool, config.Default(), nil, true, env("PATH", winApps+":"+normal), "linux")
	if len(got) != 2 {
		t.Fatalf("got %d installs, want 2", len(got))
	}
	if got[len(got)-1].Origin != "WindowsApps" {
		t.Fatalf("low-priority origin not last: %+v", got)
	}
}

func TestUnusableInstallStillReportedWithValidateAll(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "mytool", "#!/bin/sh\nexit 1\n")
	got := FindInstalls(myTool, config.Default(), nil, true, env("PATH", dir), "linux")
	if len(got) != 1 || got[0].OK {
		t.Fatalf("got %+v, want one unusable install", got)
	}
	if got[0].Note == "" {
		t.Fatal("unusable install has no note")
	}
}

// The probe cache used to key on the path alone, so a verdict reached with one
// set of arguments was served for every other set. That silently made apt-get
// look unusable — apt-get with no arguments exits 1 — and it meant that changing
// a tool's VersionArgs was ignored forever on any machine that had already
// probed it, because the file's mtime and size had not changed.
func TestProbeCacheSeparatesArgumentSets(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mgr")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'mgr 1.2.3'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	noArgs := catalog.Tool{ID: "mgr", Names: []string{"mgr"}}
	withVersion := catalog.Tool{ID: "mgr", Names: []string{"mgr"}, VersionArgs: []string{"--version"}}

	// A cached failure must not be reused for a different argument set.
	c := NewProbeCache("")
	if Probe(noArgs, exe, c, 5*time.Second).OK {
		t.Fatal("the no-argument probe should have failed")
	}
	got := Probe(withVersion, exe, c, 5*time.Second)
	if !got.OK {
		t.Error("a cached failure was reused for a different argument set")
	}
	if got.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", got.Version)
	}

	// And the reverse order must hold as well.
	c2 := NewProbeCache("")
	if !Probe(withVersion, exe, c2, 5*time.Second).OK {
		t.Fatal("the version probe should have succeeded")
	}
	if Probe(noArgs, exe, c2, 5*time.Second).OK {
		t.Error("a cached success was reused for a different argument set")
	}
}

// Two different tools that happen to resolve to the same executable must not
// share a verdict either, which the argument set is what distinguishes.
func TestProbeKeyIncludesArguments(t *testing.T) {
	if probeKey("/bin/x", nil) != "/bin/x" {
		t.Error("an empty argument set should key on the path alone")
	}
	if probeKey("/bin/x", []string{"--version"}) == probeKey("/bin/x", []string{"-v"}) {
		t.Error("different argument sets produced the same key")
	}
	if probeKey("/bin/x", []string{"--version"}) == probeKey("/bin/x", nil) {
		t.Error("an argument set collided with the empty one")
	}
}
