package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindInDirCachedMatchesFindInDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit test")
	}
	dir := t.TempDir()
	for _, n := range []string{"aaa", "bbb", "ccc"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notexec"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	names := []string{"aaa", "bbb", "ccc", "notexec", "subdir", "missing"}
	want := FindInDir(dir, names, env(), "linux")
	got := FindInDirCached(dir, names, env(), "linux", NewDirCache())
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cached = %v, want %v (identical to the stat-based scan)", got, want)
	}
	if len(want) != 3 {
		t.Fatalf("sanity: FindInDir = %v, want exactly the 3 executables", want)
	}
}

func TestFindInDirCachedSkipsMissingDir(t *testing.T) {
	got := FindInDirCached(filepath.Join(t.TempDir(), "nope"), []string{"gcc"}, env(), "linux", NewDirCache())
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing for a missing directory", got)
	}
}

func TestFindInDirCachedWithNilCacheFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindInDirCached(dir, []string{"tool"}, env(), "linux", nil); len(got) != 1 {
		t.Fatalf("nil cache returned %v, want the tool", got)
	}
}

func TestDirCacheIsPerRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "first"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := NewDirCache()
	if got := FindInDirCached(dir, []string{"first"}, env(), "linux", cache); len(got) != 1 {
		t.Fatalf("got %v, want first", got)
	}
	// A tool installed after the listing was taken is not seen by the same cache.
	if err := os.WriteFile(filepath.Join(dir, "second"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindInDirCached(dir, []string{"second"}, env(), "linux", cache); len(got) != 0 {
		t.Fatalf("got %v, want the cached listing to be reused", got)
	}
	// A fresh cache (the next run) sees it.
	if got := FindInDirCached(dir, []string{"second"}, env(), "linux", NewDirCache()); len(got) != 1 {
		t.Fatalf("got %v, want second to be found with a fresh cache", got)
	}
}

func TestFindInDirCachedReturnsTheRealEntryName(t *testing.T) {
	dir := t.TempDir()
	// A Windows-style listing. The case-insensitive match must hand back the
	// name as it is on disk, not the lowercased candidate.
	if err := os.WriteFile(filepath.Join(dir, "GCC.EXE"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindInDirCached(dir, []string{"gcc"}, env("PATHEXT", ".COM;.EXE"), "windows", NewDirCache())
	if len(got) != 1 || got[0] != filepath.Join(dir, "GCC.EXE") {
		t.Fatalf("got %v, want [%s]", got, filepath.Join(dir, "GCC.EXE"))
	}
}

func TestDirCacheListsADirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := NewDirCache()
	first, ok := cache.List(dir, false)
	if !ok || len(first) != 1 {
		t.Fatalf("List = (%v,%v), want one entry", first, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, "later"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, _ := cache.List(dir, false)
	if len(second) != 1 {
		t.Fatalf("second List saw %d entries, want the memoised 1", len(second))
	}
	fresh, _ := NewDirCache().List(dir, false)
	if len(fresh) != 2 {
		t.Fatalf("a fresh cache saw %d entries, want 2", len(fresh))
	}
}
