package jsonc

import (
	"strings"
	"testing"
)

const (
	begin = "// >>> zedrun"
	end   = "// <<< zedrun"
	block = `  {"label": "zedrun: Run File"}`
)

func mustLoad(t *testing.T, s string) any {
	t.Helper()
	v, err := Loads(s)
	if err != nil {
		t.Fatalf("Loads failed: %v\ntext:\n%s", err, s)
	}
	return v
}

func TestCommentsAndTrailingCommasParse(t *testing.T) {
	src := `{
  // a line comment
  "a": 1,
  "b": [1, 2, 3,], /* a block comment */
}`
	m := mustLoad(t, src).(map[string]any)
	if m["a"].(float64) != 1 {
		t.Fatalf("a = %v, want 1", m["a"])
	}
	if len(m["b"].([]any)) != 3 {
		t.Fatalf("b = %v, want 3 elements", m["b"])
	}
}

func TestSlashesInsideStringsKept(t *testing.T) {
	src := `{"url": "http://x//y", "note": "/* not a comment */"}`
	m := mustLoad(t, src).(map[string]any)
	if m["url"] != "http://x//y" {
		t.Fatalf("url = %v", m["url"])
	}
	if m["note"] != "/* not a comment */" {
		t.Fatalf("note = %v", m["note"])
	}
}

func TestBlankCommentsIsOffsetPreserving(t *testing.T) {
	src := "{\n  // hi\n  \"a\": 1\n}"
	got := BlankComments(src)
	if len(got) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(got))
	}
	if strings.Contains(got, "//") {
		t.Fatalf("comment not blanked: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Fatalf("line structure changed: %q", got)
	}
	if !HasComments(src) {
		t.Fatal("HasComments(src) = false, want true")
	}
	if HasComments(got) {
		t.Fatal("HasComments(blanked) = true, want false")
	}
	if HasComments(`{"a": 1}`) {
		t.Fatal("HasComments on plain JSON = true, want false")
	}
}

func TestAppendToEmptyCreatesArray(t *testing.T) {
	got := AppendManagedBlock("[]", block, begin, end)
	arr, ok := mustLoad(t, got).([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("Loads = %#v, want 1 element\ntext:\n%s", mustLoad(t, got), got)
	}
	if !strings.Contains(got, begin) || !strings.Contains(got, end) {
		t.Fatalf("markers missing:\n%s", got)
	}
}

func TestAppendAddsMissingComma(t *testing.T) {
	src := "[\n  {\"a\": 1}\n]"
	got := AppendManagedBlock(src, block, begin, end)
	arr := mustLoad(t, got).([]any)
	if len(arr) != 2 {
		t.Fatalf("len = %d, want 2\ntext:\n%s", len(arr), got)
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	once := AppendManagedBlock("[\n  {\"a\": 1}\n]", block, begin, end)
	twice := AppendManagedBlock(once, block, begin, end)
	if once != twice {
		t.Fatalf("not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
	if arr := mustLoad(t, twice).([]any); len(arr) != 2 {
		t.Fatalf("len = %d, want 2", len(arr))
	}
}

func TestAppendAfterTrailingCommentNoComma(t *testing.T) {
	// Review Focus 1: the last element is followed by a comment and no comma.
	src := "[\n  {\"a\": 1}\n  // keep me\n]"
	got := AppendManagedBlock(src, block, begin, end)
	v, err := Loads(got)
	if err != nil {
		t.Fatalf("invalid JSON after append: %v\ntext:\n%s", err, got)
	}
	arr := v.([]any)
	if len(arr) != 2 {
		t.Fatalf("len = %d, want 2\ntext:\n%s", len(arr), got)
	}
	if !strings.Contains(got, "// keep me") {
		t.Fatalf("user comment lost:\n%s", got)
	}
}

func TestRemoveRestoresOriginal(t *testing.T) {
	src := "[\n  {\"a\": 1}\n]\n"
	back := RemoveManagedBlock(AppendManagedBlock(src, block, begin, end), begin, end)
	if back != src {
		t.Fatalf("byte mismatch:\nwant: %q\ngot:  %q", src, back)
	}
}

func TestRemoveRestoresOriginalSemantically(t *testing.T) {
	for _, src := range []string{"[]", "[\n]", "[\n  {\"a\": 1}\n]\n", "[\r\n  {\"a\": 1}\r\n]\r\n"} {
		back := RemoveManagedBlock(AppendManagedBlock(src, block, begin, end), begin, end)
		if len(mustLoad(t, back).([]any)) != len(mustLoad(t, src).([]any)) {
			t.Fatalf("semantic mismatch for %q -> %q", src, back)
		}
	}
}

func TestRemoveNoBlockIsIdentity(t *testing.T) {
	src := "[\n  {\"a\": 1}\n]\n"
	if got := RemoveManagedBlock(src, begin, end); got != src {
		t.Fatalf("RemoveManagedBlock with no block changed the text: %q", got)
	}
}

func TestCRLFPreserved(t *testing.T) {
	src := "[\r\n  {\"a\": 1}\r\n]\r\n"
	got := AppendManagedBlock(src, block, begin, end)
	if !strings.Contains(got, "\r\n") {
		t.Fatalf("CRLF lost:\n%q", got)
	}
	if len(mustLoad(t, got).([]any)) != 2 {
		t.Fatalf("len != 2\ntext:\n%s", got)
	}
}
