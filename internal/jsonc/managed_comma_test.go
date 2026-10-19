package jsonc

import (
	"strings"
	"testing"
)

// A trailing comma in the array is valid JSONC, and the managed-block append
// must not add a second one. It did: the separator test only excluded '[', so
// an array whose last element already ended in a comma got ",," — which the
// package's own loader then rejects. The Python original guards this case
// explicitly (jsonc.py: need_comma = blanked[i] != ",").
func TestAppendManagedBlockWithATrailingComma(t *testing.T) {
	const begin, end = "// >>> zedrun-go", "// <<< zedrun-go"
	region := "\n  {\"z\": 1}\n"

	for _, tc := range []struct{ name, in string }{
		{"trailing comma", "[\n  {\"a\": 1},\n]"},
		{"trailing comma then a comment", "[\n  {\"a\": 1}, // keep\n]"},
		{"comment then a trailing comma", "[\n  {\"a\": 1} // keep\n,\n]"},
		{"two elements, trailing comma", "[\n  {\"a\": 1},\n  {\"b\": 2},\n]"},
		{"no trailing comma", "[\n  {\"a\": 1}\n]"},
		{"no trailing comma, comment", "[\n  {\"a\": 1} // keep\n]"},
		{"empty array", "[]"},
		{"empty array with newline", "[\n]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := AppendManagedBlock(tc.in, region, begin, end)
			if strings.Contains(out, ",,") {
				t.Fatalf("double comma produced:\n%s", out)
			}
			if _, err := Loads(out); err != nil {
				t.Fatalf("result does not parse: %v\n%s", err, out)
			}
			// and the block must actually be there
			if !strings.Contains(out, begin) || !strings.Contains(out, end) {
				t.Fatalf("managed block missing:\n%s", out)
			}
		})
	}
}

// The append/remove pair has to be a round trip, trailing comma or not.
func TestAppendThenRemoveWithATrailingComma(t *testing.T) {
	const begin, end = "// >>> zedrun-go", "// <<< zedrun-go"
	for _, in := range []string{
		"[\n  {\"a\": 1},\n]",
		"[\n  {\"a\": 1}\n]",
		"[]",
	} {
		out := RemoveManagedBlock(AppendManagedBlock(in, "\n  {\"z\": 1}\n", begin, end), begin, end)
		if _, err := Loads(out); err != nil {
			t.Errorf("round trip does not parse: %v\nin:  %q\nout: %q", err, in, out)
		}
	}
}
