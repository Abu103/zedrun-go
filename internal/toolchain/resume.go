package toolchain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/platform"
)

// SchemaVersion is bumped when the state file changes shape incompatibly. A
// file with a different version is treated as unreadable and ignored, which
// sends the next session back to diagnosis instead of misreading it.
const SchemaVersion = 1

// States a resume file moves through.
const (
	StateDiagnosing = "diagnosing"
	StateRepairing  = "repairing"
	StateVerifying  = "verifying"
	StateDone       = "done"
)

// ErrCorrupt means a state file exists but cannot be trusted: truncated by a
// crash mid-write, hand-edited into invalid JSON, or from another schema.
var ErrCorrupt = errors.New("resume state is unreadable")

// ToolRecord is what was proven about one tool the last time it was checked.
type ToolRecord struct {
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Origin  string `json:"origin,omitempty"`
}

// Subject is one problem being worked, from symptom to verified fix.
type Subject struct {
	ID         int    `json:"id"`
	Tool       string `json:"tool,omitempty"`
	Symptom    string `json:"symptom"`
	Cause      string `json:"cause,omitempty"`
	Fix        string `json:"fix,omitempty"`
	Verify     string `json:"verify,omitempty"`
	Status     string `json:"status"` // pending | done
	LastAction string `json:"lastAction,omitempty"`
	LastResult string `json:"lastResult,omitempty"`
}

// Resume is the per-project handoff record. A second account reads it, picks
// up the first pending subject, and continues without re-asking anything.
type Resume struct {
	Schema         int                   `json:"schema"`
	Project        string                `json:"project"`
	Machine        string                `json:"machine,omitempty"`
	Shell          string                `json:"shell,omitempty"`
	Toolchain      map[string]ToolRecord `json:"toolchain,omitempty"`
	Subjects       []Subject             `json:"subjects"`
	LastSubmission int                   `json:"lastSubmission"`
	Pending        []string              `json:"pending"`
	State          string                `json:"state"`
	Updated        string                `json:"updated,omitempty"`
}

// New returns an empty record for a project.
func New(project string) Resume {
	return Resume{
		Schema:    SchemaVersion,
		Project:   project,
		Toolchain: map[string]ToolRecord{},
		Subjects:  []Subject{},
		Pending:   []string{},
		State:     StateDiagnosing,
	}
}

// ProjectSlug makes a project name safe to use as a file name. Anything outside
// letters, digits, dot, dash and underscore becomes a dash, so a hostile or
// merely odd directory name cannot escape the state directory.
func ProjectSlug(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), ".-")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		return "default"
	}
	return s
}

// StatePath is where a project's resume file lives.
//
//	Windows:     %LOCALAPPDATA%\zedrun-go\toolchain\<project>.json
//	Linux/macOS: $XDG_STATE_HOME/zedrun-go/toolchain/<project>.json
//	             (default ~/.local/state/zedrun-go/toolchain/<project>.json)
func StatePath(env map[string]string, osName, project string) string {
	file := ProjectSlug(project) + ".json"
	if osName == "windows" {
		base := platform.Env(env, osName, "LOCALAPPDATA")
		if base == "" {
			if home := platform.Env(env, osName, "USERPROFILE"); home != "" {
				base = home + `\AppData\Local`
			}
		}
		if base == "" {
			return ""
		}
		return strings.TrimRight(base, `\`) + `\zedrun-go\toolchain\` + file
	}
	base := env["XDG_STATE_HOME"]
	if base == "" {
		if env["HOME"] == "" {
			return ""
		}
		base = filepath.Join(env["HOME"], ".local", "state")
	}
	return filepath.Join(base, "zedrun-go", "toolchain", file)
}

// StateDir is the directory StatePath files live in.
func StateDir(env map[string]string, osName string) string {
	p := StatePath(env, osName, "x")
	if p == "" {
		return ""
	}
	return DirOf(p, osName)
}

// Save writes the record atomically: the bytes go to a temp file in the same
// directory, are flushed to disk, and only then renamed over the target. A
// crash at any point leaves either the old complete file or the new complete
// file, never a half-written one that the next session would misread.
func (r *Resume) Save(path string) error {
	if path == "" {
		return errors.New("no state path: HOME or LOCALAPPDATA is not set")
	}
	r.Schema = SchemaVersion
	r.Updated = time.Now().UTC().Format(time.RFC3339)
	r.recompute()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".resume-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Load reads a state file. ok is false with a nil error when there is none, and
// false with ErrCorrupt when the file cannot be trusted; either way the caller
// starts from diagnosis, which is what the protocol says to do.
func Load(path string) (r Resume, ok bool, err error) {
	if path == "" {
		return Resume{}, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Resume{}, false, nil
		}
		return Resume{}, false, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&r); err != nil {
		return Resume{}, false, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if dec.More() {
		return Resume{}, false, fmt.Errorf("%w: trailing data", ErrCorrupt)
	}
	if r.Schema != SchemaVersion {
		return Resume{}, false, fmt.Errorf("%w: schema %d, want %d", ErrCorrupt, r.Schema, SchemaVersion)
	}
	if r.Toolchain == nil {
		r.Toolchain = map[string]ToolRecord{}
	}
	return r, true, nil
}

// LoadOrNew returns the saved record, or a fresh one when there is none or it
// is unreadable. The error is reported so the caller can say a file was ignored.
func LoadOrNew(path, project string) (Resume, error) {
	r, ok, err := Load(path)
	if ok {
		return r, nil
	}
	return New(project), err
}

// AddSubject records a new problem and queues it. It returns the subject id.
func (r *Resume) AddSubject(s Subject) int {
	s.ID = 1
	for _, e := range r.Subjects {
		if e.ID >= s.ID {
			s.ID = e.ID + 1
		}
	}
	if s.Status == "" {
		s.Status = "pending"
	}
	r.Subjects = append(r.Subjects, s)
	r.recompute()
	return s.ID
}

// Complete marks a subject done and records what was done and what proved it.
func (r *Resume) Complete(id int, action, result string) bool {
	for i := range r.Subjects {
		if r.Subjects[i].ID == id {
			r.Subjects[i].Status = "done"
			r.Subjects[i].LastAction = action
			r.Subjects[i].LastResult = result
			r.recompute()
			return true
		}
	}
	return false
}

// NextPending returns the first unfinished subject, the one a new account
// should re-verify before touching anything else.
func (r Resume) NextPending() (Subject, bool) {
	for _, p := range r.Pending {
		id, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(p, "subject")))
		if err != nil {
			continue
		}
		for _, s := range r.Subjects {
			if s.ID == id && s.Status != "done" {
				return s, true
			}
		}
	}
	return Subject{}, false
}

// recompute rebuilds Pending from the subjects and settles State, so the two
// can never disagree after an edit.
func (r *Resume) recompute() {
	r.Pending = r.Pending[:0]
	for _, s := range r.Subjects {
		if s.Status != "done" {
			r.Pending = append(r.Pending, "subject "+strconv.Itoa(s.ID))
		}
	}
	if len(r.Subjects) > 0 && len(r.Pending) == 0 {
		r.State = StateDone
	} else if r.State == StateDone && len(r.Pending) > 0 {
		r.State = StateDiagnosing
	}
	if r.Pending == nil {
		r.Pending = []string{}
	}
}

// Record folds one diagnosis into the record: what the tool is now, and the
// subject for whatever is wrong with it. Re-recording the same open problem
// updates it rather than queueing a duplicate, and a tool that now verifies
// closes its open subjects.
func (r *Resume) Record(st Status, d Diagnosis, p Plan, machine string) {
	if machine != "" {
		r.Machine = machine
	}
	if st.Shell != ShellUnknown {
		r.Shell = string(st.Shell)
	}
	if st.Active != nil {
		r.Toolchain[st.Tool] = ToolRecord{Version: st.Active.Version, Path: st.Active.Path, Origin: st.Active.Origin}
	}
	if d.Kind == KindOK {
		for i := range r.Subjects {
			if r.Subjects[i].Tool == st.Tool && r.Subjects[i].Status != "done" {
				r.Subjects[i].Status = "done"
				r.Subjects[i].LastAction = "verify"
				r.Subjects[i].LastResult = d.Symptom
			}
		}
		r.recompute()
		return
	}
	fix, verify := "", ""
	if len(p.Steps) > 0 {
		fix = p.Steps[0].Command
	}
	if len(p.Verify) > 0 {
		verify = p.Verify[0]
	}
	for i := range r.Subjects {
		s := &r.Subjects[i]
		if s.Tool == st.Tool && s.Status != "done" {
			s.Symptom, s.Cause, s.Fix, s.Verify = d.Symptom, d.Cause, fix, verify
			r.State = StateRepairing
			r.recompute()
			return
		}
	}
	r.AddSubject(Subject{Tool: st.Tool, Symptom: d.Symptom, Cause: d.Cause, Fix: fix, Verify: verify})
	r.State = StateRepairing
}
