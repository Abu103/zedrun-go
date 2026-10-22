package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/detect"
)

func sample() Resume {
	r := New("zedrun-go")
	r.Machine = "pc-alex\\alex"
	r.Shell = "powershell"
	r.Toolchain["go"] = ToolRecord{Version: "1.24.0", Path: `C:\Go\bin\go.exe`, Origin: "winget"}
	id := r.AddSubject(Subject{Tool: "gcc", Symptom: "gcc not found", Fix: "zedrun-go toolchain repair gcc", Verify: "gcc --version"})
	r.AddSubject(Subject{Tool: "node", Symptom: "node not on PATH"})
	r.Complete(id, "repair", "gcc 13.2 verified")
	r.LastSubmission = 12
	return r
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "p.json")
	r := sample()
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Project != "zedrun-go" || got.LastSubmission != 12 || got.Toolchain["go"].Version != "1.24.0" || len(got.Subjects) != 2 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Schema != SchemaVersion || got.Updated == "" {
		t.Fatalf("schema/updated not stamped: %+v", got)
	}
}

// Review focus: a half-written state file is ignored, not misread.
func TestHalfWrittenStateFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	r := sample()
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	full, _ := os.ReadFile(path)

	for _, cut := range []int{1, len(full) / 3, len(full) / 2, len(full) - 3} {
		if err := os.WriteFile(path, full[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		got, ok, err := Load(path)
		if ok {
			t.Fatalf("a file cut at %d bytes was accepted: %+v", cut, got)
		}
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("cut at %d: err = %v, want ErrCorrupt", cut, err)
		}
	}
	// The caller falls back to a fresh record rather than failing.
	fresh, err := LoadOrNew(path, "zedrun-go")
	if !errors.Is(err, ErrCorrupt) || fresh.State != StateDiagnosing || len(fresh.Subjects) != 0 {
		t.Fatalf("LoadOrNew = %+v, %v; want a fresh record and the corruption reported", fresh, err)
	}
}

func TestTrailingGarbageAndWrongSchemaAreCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	r := sample()
	_ = r.Save(path)
	full, _ := os.ReadFile(path)

	_ = os.WriteFile(path, append(full, []byte(`{"extra":1}`)...), 0o644)
	if _, ok, err := Load(path); ok || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("trailing data accepted: ok=%v err=%v", ok, err)
	}
	_ = os.WriteFile(path, []byte(`{"schema":99,"subjects":[],"pending":[],"state":"done"}`), 0o644)
	if _, ok, err := Load(path); ok || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("future schema accepted: ok=%v err=%v", ok, err)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	_, ok, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
}

func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	// An old, valid file is replaced whole, and a stray half-written file
	// at the target is overwritten by a complete one.
	_ = os.WriteFile(path, []byte(`{"schema":1,"sub`), 0o644)
	r := sample()
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Load(path); !ok || err != nil {
		t.Fatalf("after save: ok=%v err=%v", ok, err)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if len(ents) != 1 {
		t.Fatalf("want exactly the state file, got %d entries", len(ents))
	}
}

func TestStrayTempFileDoesNotAffectLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	r := sample()
	_ = r.Save(path)
	// A crash mid-write leaves a temp file next to the real one.
	_ = os.WriteFile(filepath.Join(dir, ".resume-123.tmp"), []byte(`{"half`), 0o644)
	if _, ok, err := Load(path); !ok || err != nil {
		t.Fatalf("a crashed writer's temp file broke the real state: ok=%v err=%v", ok, err)
	}
}

func TestSaveFailureKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	r := sample()
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	// Make the directory read-only so the temp file cannot be created.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod")
	}
	defer os.Chmod(dir, 0o755)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	r.LastSubmission = 99
	if err := r.Save(path); err == nil {
		t.Fatal("save into a read-only directory succeeded")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a failed save damaged the existing state file")
	}
}

func TestStatePathLocations(t *testing.T) {
	if got := StatePath(map[string]string{"XDG_STATE_HOME": "/s"}, "linux", "proj"); got != "/s/zedrun-go/toolchain/proj.json" {
		t.Errorf("xdg: %s", got)
	}
	if got := StatePath(map[string]string{"HOME": "/h"}, "linux", "proj"); got != "/h/.local/state/zedrun-go/toolchain/proj.json" {
		t.Errorf("home default: %s", got)
	}
	if got := StatePath(map[string]string{"HOME": "/h"}, "macos", "proj"); got != "/h/.local/state/zedrun-go/toolchain/proj.json" {
		t.Errorf("macos follows the skill's XDG rule: %s", got)
	}
	if got := StatePath(map[string]string{"LOCALAPPDATA": `C:\Users\a\AppData\Local`}, "windows", "proj"); got != `C:\Users\a\AppData\Local\zedrun-go\toolchain\proj.json` {
		t.Errorf("windows: %s", got)
	}
	if got := StatePath(map[string]string{"localappdata": `C:\L`}, "windows", "p"); got != `C:\L\zedrun-go\toolchain\p.json` {
		t.Errorf("windows env names are case-insensitive: %s", got)
	}
	if StatePath(map[string]string{}, "linux", "p") != "" {
		t.Error("no HOME must give no path rather than a relative one")
	}
}

func TestProjectSlugCannotEscape(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", `..\..\Windows`, "a/b", "", "...", "my project!"} {
		s := ProjectSlug(in)
		if strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".") || s == "" {
			t.Errorf("ProjectSlug(%q) = %q is unsafe", in, s)
		}
	}
	if ProjectSlug("zedrun-go") != "zedrun-go" {
		t.Error("a normal name changed")
	}
}

// The handoff: a second account reads the file, takes the first pending
// subject, finishes it, and the file reports done.
func TestHandoffProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	first := New("proj")
	first.AddSubject(Subject{Tool: "gcc", Symptom: "gcc missing"})
	first.AddSubject(Subject{Tool: "node", Symptom: "node off PATH"})
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}

	second, ok, err := Load(path)
	if !ok || err != nil {
		t.Fatal(err)
	}
	s, ok := second.NextPending()
	if !ok || s.Tool != "gcc" {
		t.Fatalf("next pending = %+v, want gcc first", s)
	}
	second.Complete(s.ID, "repair", "gcc 13 verified")
	if s, _ = second.NextPending(); s.Tool != "node" {
		t.Fatalf("after finishing gcc, next = %+v", s)
	}
	second.Complete(s.ID, "path", "node 20 verified")
	if _, ok := second.NextPending(); ok || second.State != StateDone || len(second.Pending) != 0 {
		t.Fatalf("not done: %+v", second)
	}
	if err := second.Save(path); err != nil {
		t.Fatal(err)
	}
	third, _, _ := Load(path)
	if third.State != StateDone || third.Subjects[0].LastResult != "gcc 13 verified" {
		t.Fatalf("done state not persisted: %+v", third)
	}
}

func TestRecordDedupesAndClosesOnOK(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	bad := Assemble(Inputs{Tool: "go", OS: "linux", Env: env, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	d := Diagnose(bad, Options{ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }})
	p := Repair(bad, d, Options{})

	r := New("proj")
	r.Record(bad, d, p, "host")
	r.Record(bad, d, p, "host") // same open problem again
	if len(r.Subjects) != 1 || len(r.Pending) != 1 || r.State != StateRepairing {
		t.Fatalf("duplicate subject queued: %+v", r)
	}

	good := Assemble(Inputs{Tool: "go", OS: "linux", Env: map[string]string{"PATH": "/opt/go/bin"}, Installs: []detect.Install{inst("/opt/go/bin/go", true)}})
	r.Record(good, Diagnose(good, Options{}), Plan{}, "host")
	if r.State != StateDone || len(r.Pending) != 0 || r.Toolchain["go"].Path != "/opt/go/bin/go" {
		t.Fatalf("a verified tool did not close its subject: %+v", r)
	}
}

// detect is imported by the test helpers in toolchain_test.go.
