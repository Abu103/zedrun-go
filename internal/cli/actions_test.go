package cli

import (
	"bytes"
	"testing"

	"github.com/zedrun/zedrun-go/internal/sources"
)

// The dashboard is launched with no fetcher, so every update check used to
// report "no version source configured". cmdTUI must build one, exactly as
// cmdOutdated does.
func TestTUIFetcherBuildsOneWhenTheCallerSuppliedNone(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	if f := tuiFetcher(Options{}, env, "linux"); f == nil {
		t.Fatal("tuiFetcher returned nil: the dashboard's update check would be dead")
	}
}

func TestTUIFetcherKeepsTheInjectedOne(t *testing.T) {
	injected := sources.NewFetcher("", 0, nil)
	if f := tuiFetcher(Options{Fetcher: injected}, map[string]string{}, "linux"); f != injected {
		t.Fatal("tuiFetcher replaced an injected fetcher; tests and callers would lose their seam")
	}
}

// Options.File is the dashboard's first source for `r`, and nothing else sets
// it — so --file is what makes that fallback reachable.
func TestParseFileFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantFile string
		wantRest []string
		wantErr  bool
	}{
		{"absent", []string{"doctor"}, "", []string{"doctor"}, false},
		{"none at all", nil, "", nil, false},
		{"separate value", []string{"--file", "/tmp/a.py"}, "/tmp/a.py", []string{}, false},
		{"equals form", []string{"--file=/tmp/a.py"}, "/tmp/a.py", []string{}, false},
		{"leaves the rest", []string{"--file", "/tmp/a.py", "run", "b.py"}, "/tmp/a.py", []string{"run", "b.py"}, false},
		{"missing value", []string{"--file"}, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, rest, err := parseFileFlag(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if file != tc.wantFile {
				t.Errorf("file = %q, want %q", file, tc.wantFile)
			}
			if len(rest) != len(tc.wantRest) {
				t.Fatalf("rest = %v, want %v", rest, tc.wantRest)
			}
			for i := range rest {
				if rest[i] != tc.wantRest[i] {
					t.Errorf("rest = %v, want %v", rest, tc.wantRest)
				}
			}
		})
	}
}

// `--file X` with nothing else opens the dashboard, and must not fall through
// to the unknown-command path.
func TestFileFlagAloneOpensTheDashboard(t *testing.T) {
	var out, errOut bytes.Buffer
	code := MainWith([]string{"--file", "/tmp/a.py"}, &out, &errOut, Options{
		Env: map[string]string{"HOME": t.TempDir()},
		OS:  "linux",
	})
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if errOut.String() != "" {
		t.Errorf("stderr: %s", errOut.String())
	}
}

// A bad --file must be reported, not silently ignored.
func TestFileFlagWithoutAValueIsAnError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := MainWith([]string{"--file"}, &out, &errOut, Options{}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
