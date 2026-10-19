package platform

import (
	"reflect"
	"testing"
)

// A Windows environment block keeps whatever casing the parent process used,
// and Windows lookups are case-insensitive. This machine stores it as `path`,
// so `env["PATH"]` returned "" and the entire PATH was skipped: every tool that
// is only on PATH — python, go, node — was invisible to the Windows build.
func TestEnvIsCaseInsensitiveOnWindows(t *testing.T) {
	for _, key := range []string{"PATH", "Path", "path", "pAtH"} {
		e := env(key, `C:\Python313;C:\Program Files\Go\bin`)
		if got := Env(e, "windows", "PATH"); got != e[key] {
			t.Errorf("Env(env, windows, \"PATH\") with key %q = %q, want %q", key, got, e[key])
		}
	}
}

// On Unix they are genuinely different variables, so the lookup must stay exact.
func TestEnvStaysCaseSensitiveOnUnix(t *testing.T) {
	e := env("path", "/usr/bin")
	if got := Env(e, "linux", "PATH"); got != "" {
		t.Errorf(`Env(env, linux, "PATH") = %q, want "" — on Unix path is not PATH`, got)
	}
	if got := Env(e, "linux", "path"); got != "/usr/bin" {
		t.Errorf(`Env(env, linux, "path") = %q, want /usr/bin`, got)
	}
}

func TestEnvPrefersAnExactMatch(t *testing.T) {
	e := env("PATH", "/exact", "path", "/folded")
	if got := Env(e, "windows", "PATH"); got != "/exact" {
		t.Errorf("Env = %q, want the exact-case value", got)
	}
}

// The regression that matters: PATH discovery must work when the key is
// lowercase, which is what Windows actually hands the process.
func TestPathEntriesReadsALowercasePathKey(t *testing.T) {
	e := env("path", `C:\Python313;"C:\Program Files\Go\bin";;C:\Program Files\nodejs`)
	got := PathEntries(e, "windows")
	want := []string{`C:\Python313`, `C:\Program Files\Go\bin`, `C:\Program Files\nodejs`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PathEntries = %q, want %q", got, want)
	}
}

func TestPathEntriesStillWorksWithAnUppercaseKey(t *testing.T) {
	e := env("PATH", `C:\Python313;C:\Program Files\Go\bin`)
	got := PathEntries(e, "windows")
	want := []string{`C:\Python313`, `C:\Program Files\Go\bin`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PathEntries = %q, want %q", got, want)
	}
}

func TestExeExtsReadsALowercasePathextKey(t *testing.T) {
	e := env("pathext", ".COM;.EXE;.BAT")
	got := ExeExts(e, "windows")
	want := []string{".com", ".exe", ".bat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExeExts = %q, want %q", got, want)
	}
}

// The same class of bug reached every other Windows variable the tool reads.
func TestWindowsPathVariablesAreCaseInsensitive(t *testing.T) {
	e := env(
		"userprofile", `C:\Users\mudas`,
		"appdata", `C:\Users\mudas\AppData\Roaming`,
		"localappdata", `C:\Users\mudas\AppData\Local`,
	)
	if got := home(e, "windows"); got != `C:\Users\mudas` {
		t.Errorf("home = %q", got)
	}
	if got := appData(e, "windows"); got != `C:\Users\mudas\AppData\Roaming` {
		t.Errorf("appData = %q", got)
	}
	if got := localAppData(e, "windows"); got != `C:\Users\mudas\AppData\Local` {
		t.Errorf("localAppData = %q", got)
	}
}

func TestWindowsDriveAndHomePathAreCaseInsensitive(t *testing.T) {
	e := env("homedrive", `C:`, "homepath", `\Users\mudas`)
	if got := home(e, "windows"); got != `C:\Users\mudas` {
		t.Errorf("home = %q, want C:\\Users\\mudas", got)
	}
}
