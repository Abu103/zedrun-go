// Package installer writes zedrun-go's tasks and keybindings into Zed's config
// by text insertion inside a managed block, so the user's own entries, comments
// and formatting survive. It backs up once, refuses to rebind a key the user
// already uses, and never touches a file it cannot parse.
package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zedrun/zedrun-go/internal/jsonc"
	"github.com/zedrun/zedrun-go/internal/platform"
)

// Markers delimit the region zedrun-go owns in a Zed config file.
const (
	BeginMarker = "// >>> zedrun-go"
	EndMarker   = "// <<< zedrun-go"
)

// TaskSpec is one Zed task to install.
type TaskSpec struct {
	Label string
	Args  []string
}

// DefaultTasks are installed into Zed's tasks.json.
var DefaultTasks = []TaskSpec{
	{"zedrun-go: Run File", []string{"run", "$ZED_FILE"}},
	{"zedrun-go: Run File (release)", []string{"run", "--release", "$ZED_FILE"}},
	{"zedrun-go: Pick Toolchain", []string{"pick", "$ZED_FILE"}},
	{"zedrun-go: Doctor", []string{"doctor"}},
	{"zedrun-go: Check Updates", []string{"outdated"}},
	{"zedrun-go: Check Updates (pre-release)", []string{"outdated", "--pre"}},
}

// DefaultKeys maps a key to the task label it runs. The Editor context is lower
// in Zed's context tree than Workspace, so these win while editing without
// stealing the debugger's F6/F7.
var DefaultKeys = map[string]string{
	"f6":       "zedrun-go: Run File",
	"shift-f6": "zedrun-go: Run File (release)",
	"f7":       "zedrun-go: Pick Toolchain",
	"shift-f7": "zedrun-go: Doctor",
	"ctrl-f7":  "zedrun-go: Check Updates",
}

// Install writes the tasks and keybindings. With dryRun it reports what it would
// do and writes nothing.
func Install(zedDir, binPath string, dryRun bool, out io.Writer) ([]string, error) {
	var msgs []string
	emit := func(s string) {
		msgs = append(msgs, s)
		if out != nil {
			fmt.Fprintln(out, s)
		}
	}

	tasksPath := filepath.Join(zedDir, "tasks.json")
	block, err := taskBlockJSON(binPath)
	if err != nil {
		return msgs, err
	}
	report(tasksPath, block, dryRun, emit)

	keymapPath := filepath.Join(zedDir, "keymap.json")
	keys := sortedKeys(DefaultKeys)
	var conflicts map[string]bool
	if data, err := os.ReadFile(keymapPath); err == nil {
		conflicts = conflictingKeys(string(data), keys)
	}
	for _, k := range keys {
		if conflicts[k] {
			emit(fmt.Sprintf("skipped %s: you already bind it", k))
		}
	}
	kbBlock := keymapBlockJSON(keys, conflicts)
	if kbBlock == "" {
		emit("skipped " + keymapPath + ": every key is already bound")
		return msgs, nil
	}
	report(keymapPath, kbBlock, dryRun, emit)
	return msgs, nil
}

// report applies one file and emits what happened.
func report(path, block string, dryRun bool, emit func(string)) {
	changed, skip, err := applyFile(path, block, dryRun)
	switch {
	case err != nil:
		emit("failed " + path + ": " + err.Error())
	case skip != "":
		emit(skip)
	case changed && dryRun:
		emit("would write " + path)
	case changed:
		emit("wrote " + path)
	default:
		emit(path + " already up to date")
	}
}

// applyFile inserts block into a JSON-array file, creating it when absent.
// It returns changed=true when a write happened (or would happen), and a
// message when it deliberately left the file alone.
func applyFile(path, block string, dryRun bool) (changed bool, skip string, err error) {
	text := "[]\n"
	if data, rerr := os.ReadFile(path); rerr == nil {
		text = string(data)
	} else if !os.IsNotExist(rerr) {
		return false, "", rerr
	}
	if v, perr := jsonc.Loads(text); perr != nil {
		return false, "skipped " + filepath.Base(path) + ": it is not valid JSONC, left untouched", nil
	} else if _, ok := v.([]any); !ok {
		return false, "skipped " + filepath.Base(path) + ": its top level is not an array, left untouched", nil
	}

	updated := jsonc.AppendManagedBlock(text, block, BeginMarker, EndMarker)
	if updated == text {
		return false, "", nil
	}
	if dryRun {
		return true, "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, "", err
	}
	backupOnce(path)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// backupOnce copies the file to <name>.bak the first time only.
func backupOnce(path string) {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(bak, data, 0o644)
	}
}

// Uninstall removes the managed block from both files and drops the backups.
func Uninstall(zedDir string, purge bool) ([]string, error) {
	var msgs []string
	for _, name := range []string{"tasks.json", "keymap.json"} {
		path := filepath.Join(zedDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		if !strings.Contains(text, BeginMarker) {
			continue
		}
		updated := tidyArray(jsonc.RemoveManagedBlock(text, BeginMarker, EndMarker))
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return msgs, err
		}
		msgs = append(msgs, "cleaned "+path)
	}
	for _, name := range []string{"tasks.json.bak", "keymap.json.bak"} {
		path := filepath.Join(zedDir, name)
		if err := os.Remove(path); err == nil {
			msgs = append(msgs, "removed "+path)
		}
	}
	if purge {
		if dir := platform.DataDir(envMap(), ""); dir != "" {
			if err := os.RemoveAll(dir); err == nil {
				msgs = append(msgs, "removed "+dir)
			}
		}
	}
	return msgs, nil
}

// tidyArray collapses an emptied array back to "[]", whatever whitespace the
// removal left between the brackets.
func tidyArray(text string) string {
	stripped := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '	', '\n', '\r':
			return -1
		}
		return r
	}, jsonc.BlankComments(text))
	if stripped == "[]" || stripped == "" {
		return "[]\n"
	}
	return text
}

func taskBlockJSON(binPath string) (string, error) {
	parts := make([]string, 0, len(DefaultTasks))
	for _, t := range DefaultTasks {
		label, err := json.Marshal(t.Label)
		if err != nil {
			return "", err
		}
		cmd, err := json.Marshal(binPath)
		if err != nil {
			return "", err
		}
		args, err := json.Marshal(t.Args)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf(
			"  {\"label\": %s, \"command\": %s, \"args\": %s, \"save\": \"current\"}",
			label, cmd, args))
	}
	return strings.Join(parts, ",\n"), nil
}

func keymapBlockJSON(keys []string, conflicts map[string]bool) string {
	var binds []string
	for _, k := range keys {
		if conflicts[k] {
			continue
		}
		key, err := json.Marshal(k)
		if err != nil {
			continue
		}
		label, err := json.Marshal(DefaultKeys[k])
		if err != nil {
			continue
		}
		binds = append(binds, fmt.Sprintf("      %s: [\"task::Spawn\", {\"task_name\": %s}]", key, label))
	}
	if len(binds) == 0 {
		return ""
	}
	return "  {\n    \"context\": \"Editor\",\n    \"bindings\": {\n" +
		strings.Join(binds, ",\n") + "\n    }\n  }"
}

// conflictingKeys reports which keys the user already binds outside our block.
func conflictingKeys(text string, keys []string) map[string]bool {
	without := jsonc.RemoveManagedBlock(text, BeginMarker, EndMarker)
	blank := jsonc.BlankComments(without)
	out := map[string]bool{}
	for _, k := range keys {
		if strings.Contains(blank, `"`+k+`"`) {
			out[k] = true
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func envMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
