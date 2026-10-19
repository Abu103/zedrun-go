package installer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zedrun/zedrun-go/internal/jsonc"
)

const binPath = "/usr/local/bin/zedrun-go"

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// blockOf returns the text between the managed markers.
func blockOf(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, BeginMarker)
	if i < 0 {
		t.Fatalf("no managed block in:\n%s", text)
	}
	j := strings.Index(text[i:], EndMarker)
	if j < 0 {
		t.Fatalf("no end marker in:\n%s", text)
	}
	return text[i : i+j]
}

func install(t *testing.T, zed string, dryRun bool) []string {
	t.Helper()
	msgs, err := Install(zed, binPath, dryRun, io.Discard)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return msgs
}

func TestInstallCreatesValidFiles(t *testing.T) {
	zed := t.TempDir()
	install(t, zed, false)

	tasks := read(t, filepath.Join(zed, "tasks.json"))
	v, err := jsonc.Loads(tasks)
	if err != nil {
		t.Fatalf("tasks.json is not valid JSONC: %v\n%s", err, tasks)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != len(DefaultTasks) {
		t.Fatalf("tasks.json has %d entries, want %d\n%s", len(arr), len(DefaultTasks), tasks)
	}

	keymap := read(t, filepath.Join(zed, "keymap.json"))
	if _, err := jsonc.Loads(keymap); err != nil {
		t.Fatalf("keymap.json is not valid JSONC: %v\n%s", err, keymap)
	}
}

func TestInstallPreservesUserEntriesAndComments(t *testing.T) {
	zed := t.TempDir()
	write(t, filepath.Join(zed, "tasks.json"), "[\n  // my own task\n  {\"label\": \"mine\", \"command\": \"echo\"}\n]\n")
	install(t, zed, false)

	tasks := read(t, filepath.Join(zed, "tasks.json"))
	if !strings.Contains(tasks, "// my own task") {
		t.Fatalf("user comment lost:\n%s", tasks)
	}
	if !strings.Contains(tasks, `"mine"`) {
		t.Fatalf("user task lost:\n%s", tasks)
	}
	v, err := jsonc.Loads(tasks)
	if err != nil {
		t.Fatalf("tasks.json became invalid: %v\n%s", err, tasks)
	}
	if arr := v.([]any); len(arr) != len(DefaultTasks)+1 {
		t.Fatalf("got %d entries, want %d\n%s", len(arr), len(DefaultTasks)+1, tasks)
	}
}

func TestInstallIdempotent(t *testing.T) {
	zed := t.TempDir()
	install(t, zed, false)
	first := read(t, filepath.Join(zed, "tasks.json"))
	install(t, zed, false)
	second := read(t, filepath.Join(zed, "tasks.json"))
	if first != second {
		t.Fatalf("install is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestConflictingKeyIsSkipped(t *testing.T) {
	zed := t.TempDir()
	write(t, filepath.Join(zed, "keymap.json"),
		"[\n  {\"context\": \"Workspace\", \"bindings\": {\"f6\": [\"debugger::Start\"]}}\n]\n")
	install(t, zed, false)

	keymap := read(t, filepath.Join(zed, "keymap.json"))
	block := blockOf(t, keymap)
	if strings.Contains(block, `"f6"`) {
		t.Fatalf("installed block rebinds a key the user already uses:\n%s", block)
	}
	if !strings.Contains(block, `"f7"`) {
		t.Fatalf("installed block dropped the free keys too:\n%s", block)
	}
	if _, err := jsonc.Loads(keymap); err != nil {
		t.Fatalf("keymap.json became invalid: %v\n%s", err, keymap)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	zed := t.TempDir()
	var buf bytes.Buffer
	msgs, err := Install(zed, binPath, true, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(zed, "tasks.json")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote tasks.json")
	}
	if _, err := os.Stat(filepath.Join(zed, "keymap.json")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote keymap.json")
	}
	if len(msgs) == 0 || !strings.Contains(buf.String(), "tasks.json") {
		t.Fatalf("dry run printed nothing useful: msgs=%v buf=%q", msgs, buf.String())
	}
}

func TestBackupCreatedOnce(t *testing.T) {
	zed := t.TempDir()
	write(t, filepath.Join(zed, "tasks.json"), "[]\n")
	install(t, zed, false)
	firstBackup := read(t, filepath.Join(zed, "tasks.json.bak"))
	if strings.TrimSpace(firstBackup) != "[]" {
		t.Fatalf("backup is not the original: %q", firstBackup)
	}
	install(t, zed, false)
	if second := read(t, filepath.Join(zed, "tasks.json.bak")); second != firstBackup {
		t.Fatalf("backup was overwritten:\n%q\n%q", firstBackup, second)
	}
}

func TestUninstallRemovesBlockAndBackup(t *testing.T) {
	zed := t.TempDir()
	install(t, zed, false)
	if _, err := Uninstall(zed, false); err != nil {
		t.Fatal(err)
	}
	tasks := read(t, filepath.Join(zed, "tasks.json"))
	if strings.Contains(tasks, BeginMarker) {
		t.Fatalf("managed block survived uninstall:\n%s", tasks)
	}
	if strings.TrimSpace(tasks) != "[]" {
		t.Fatalf("tasks.json = %q, want a clean []", tasks)
	}
	if _, err := os.Stat(filepath.Join(zed, "tasks.json.bak")); !os.IsNotExist(err) {
		t.Fatal("backup survived uninstall")
	}
}

func TestUninstallKeepsUserEntries(t *testing.T) {
	zed := t.TempDir()
	write(t, filepath.Join(zed, "tasks.json"), "[\n  // mine\n  {\"label\": \"mine\", \"command\": \"echo\"}\n]\n")
	install(t, zed, false)
	if _, err := Uninstall(zed, false); err != nil {
		t.Fatal(err)
	}
	tasks := read(t, filepath.Join(zed, "tasks.json"))
	if !strings.Contains(tasks, `"mine"`) || !strings.Contains(tasks, "// mine") {
		t.Fatalf("uninstall lost the user's entry:\n%s", tasks)
	}
}

func TestCorruptZedFileSkipped(t *testing.T) {
	zed := t.TempDir()
	write(t, filepath.Join(zed, "tasks.json"), "this is not json at all")
	msgs := install(t, zed, false)
	if got := read(t, filepath.Join(zed, "tasks.json")); got != "this is not json at all" {
		t.Fatalf("corrupt file was modified:\n%s", got)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "tasks.json") {
		t.Fatalf("no message about the skipped file: %v", msgs)
	}
	// The keymap is independent and must still be installed.
	if _, err := os.Stat(filepath.Join(zed, "keymap.json")); err != nil {
		t.Fatalf("keymap.json was not installed because tasks.json was corrupt: %v", err)
	}
}

func TestKeybindingShape(t *testing.T) {
	zed := t.TempDir()
	install(t, zed, false)
	block := blockOf(t, read(t, filepath.Join(zed, "keymap.json")))
	if !strings.Contains(block, `"context": "Editor"`) {
		t.Fatalf("keybindings are not scoped to the Editor context:\n%s", block)
	}
	if !strings.Contains(block, `["task::Spawn"`) {
		t.Fatalf("keybinding action is not the array form:\n%s", block)
	}
	if !strings.Contains(block, `"task_name"`) {
		t.Fatalf("keybinding does not name its task:\n%s", block)
	}
}

func TestTaskShape(t *testing.T) {
	zed := t.TempDir()
	install(t, zed, false)
	block := blockOf(t, read(t, filepath.Join(zed, "tasks.json")))
	for _, want := range []string{`"command"`, `"args"`, `"save": "current"`, binPath} {
		if !strings.Contains(block, want) {
			t.Errorf("task block missing %s:\n%s", want, block)
		}
	}
}
