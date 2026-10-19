package dist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Windows resource files are committed so a plain `go build` produces a
// binary Windows will keep. Losing them is not a build error — the exe still
// compiles and runs — it just gets quarantined by Defender a few seconds after
// you start using it, which is a miserable thing to debug. Defender's own
// scanner reports "found 1 threats" without them and "found no threats" with.
func TestWindowsResourcesArePresent(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{
		"resource_windows_amd64.syso",
		"resource_windows_arm64.syso",
		"versioninfo.json",
		"zedrun.manifest",
	} {
		p := filepath.Join(root, "cmd", "zedrun", name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing %s: Windows builds will be quarantined by Defender", p)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", p)
		}
	}
}

// The manifest has to ask for asInvoker and nothing higher: a code runner that
// requests elevation is both unnecessary and suspicious.
func TestManifestRequestsNoElevation(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "zedrun", "zedrun.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `level="asInvoker"`) {
		t.Error("manifest does not request asInvoker")
	}
	for _, bad := range []string{"requireAdministrator", "highestAvailable"} {
		if strings.Contains(s, bad) {
			t.Errorf("manifest requests %s", bad)
		}
	}
}

// versioninfo.json must carry the same version the binary reports, or the
// file properties in Explorer lie about which build you have.
func TestVersionInfoMatchesTheBinaryVersion(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "cmd", "zedrun", "versioninfo.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := os.ReadFile(filepath.Join(root, "internal", "version", "version.go"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = `const Version = "`
	i := strings.Index(string(v), marker)
	if i < 0 {
		t.Fatal("could not read the version constant")
	}
	rest := string(v)[i+len(marker):]
	ver := rest[:strings.Index(rest, `"`)]
	if !strings.Contains(string(b), `"ProductVersion": "`+ver+`"`) {
		t.Errorf("versioninfo.json does not carry ProductVersion %s", ver)
	}
}
