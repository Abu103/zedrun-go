package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
)

// End to end: a tool that is only reachable through PATH must be found when
// Windows hands us the environment with a lowercase key, which is what it
// actually does. Before the fix this returned nothing at all for python.
func TestCandidatePathsSeesAToolOnALowercasePathKey(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := map[string]string{"path": dir} // lowercase, as this machine really stores it

	got := CandidatePaths(catalog.GetTool("python"), config.Default(), e, "windows")
	for _, p := range got {
		if strings.EqualFold(p, exe) {
			return
		}
	}
	t.Fatalf("python.exe in a lowercase `path` entry was not found; candidates = %q", got)
}

// The same, with the casing a different Windows machine might use.
func TestCandidatePathsSeesAToolOnAnUppercasePathKey(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := map[string]string{"Path": dir}

	got := CandidatePaths(catalog.GetTool("python"), config.Default(), e, "windows")
	for _, p := range got {
		if strings.EqualFold(p, exe) {
			return
		}
	}
	t.Fatalf("python.exe in a `Path` entry was not found; candidates = %q", got)
}
