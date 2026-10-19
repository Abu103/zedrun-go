package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func env(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func TestOSNameOverride(t *testing.T) {
	if got := OSName("windows"); got != "windows" {
		t.Errorf("OSName(windows) = %q", got)
	}
	if got := OSName(""); got != runtime.GOOS && runtime.GOOS == "linux" {
		t.Errorf("OSName(\"\") = %q, want the host OS", got)
	}
}

func TestConfigDirPerOS(t *testing.T) {
	cases := []struct {
		name   string
		osName string
		env    map[string]string
		want   string
	}{
		{"windows", "windows", env("APPDATA", `C:\Users\z\AppData\Roaming`), `C:\Users\z\AppData\Roaming\zedrun`},
		{"macos", "macos", env("HOME", "/Users/z"), "/Users/z/Library/Application Support/zedrun"},
		{"linux", "linux", env("HOME", "/home/z"), "/home/z/.config/zedrun"},
		{"linux-xdg", "linux", env("HOME", "/home/z", "XDG_CONFIG_HOME", "/x/cfg"), "/x/cfg/zedrun"},
	}
	for _, c := range cases {
		if got := ConfigDir(c.env, c.osName); got != c.want {
			t.Errorf("%s: ConfigDir = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCacheAndDataDirPerOS(t *testing.T) {
	cases := []struct {
		name   string
		osName string
		env    map[string]string
		cache  string
		data   string
	}{
		{"windows", "windows", env("APPDATA", `C:\Roaming`, "LOCALAPPDATA", `C:\Local`), `C:\Local\zedrun`, `C:\Roaming\zedrun`},
		{"macos", "macos", env("HOME", "/Users/z"), "/Users/z/Library/Caches/zedrun", "/Users/z/Library/Application Support/zedrun"},
		{"linux", "linux", env("HOME", "/home/z"), "/home/z/.cache/zedrun", "/home/z/.local/share/zedrun"},
	}
	for _, c := range cases {
		if got := CacheDir(c.env, c.osName); got != c.cache {
			t.Errorf("%s: CacheDir = %q, want %q", c.name, got, c.cache)
		}
		if got := DataDir(c.env, c.osName); got != c.data {
			t.Errorf("%s: DataDir = %q, want %q", c.name, got, c.data)
		}
	}
}

func TestZedConfigDirPerOS(t *testing.T) {
	cases := []struct {
		osName string
		env    map[string]string
		want   string
	}{
		{"windows", env("APPDATA", `C:\Users\z\AppData\Roaming`), `C:\Users\z\AppData\Roaming\Zed`},
		{"macos", env("HOME", "/Users/z"), "/Users/z/Library/Application Support/Zed"},
		{"linux", env("HOME", "/home/z"), "/home/z/.config/zed"},
	}
	for _, c := range cases {
		if got := ZedConfigDir(c.env, c.osName); got != c.want {
			t.Errorf("ZedConfigDir(%s) = %q, want %q", c.osName, got, c.want)
		}
	}
}

func TestPathEntriesSplitAndStripQuotes(t *testing.T) {
	got := PathEntries(env("PATH", `"C:\Program Files\Go\bin";C:\Windows\System32;;D:\tools`), "windows")
	want := []string{`C:\Program Files\Go\bin`, `C:\Windows\System32`, `D:\tools`}
	if len(got) != len(want) {
		t.Fatalf("PathEntries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PathEntries[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got = PathEntries(env("PATH", "/usr/bin:/bin::/opt/x"), "linux")
	want = []string{"/usr/bin", "/bin", "/opt/x"}
	if len(got) != len(want) {
		t.Fatalf("posix PathEntries = %v, want %v", got, want)
	}
}

func TestExeExts(t *testing.T) {
	got := ExeExts(env("PATHEXT", ".COM;.EXE;.BAT"), "windows")
	want := []string{".com", ".exe", ".bat"}
	if len(got) != len(want) {
		t.Fatalf("ExeExts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExeExts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := ExeExts(env("PATHEXT", ""), "windows"); len(got) != 4 {
		t.Fatalf("default ExeExts = %v, want 4 entries", got)
	}
	if got := ExeExts(env(), "linux"); len(got) != 1 || got[0] != "" {
		t.Fatalf("posix ExeExts = %v, want [\"\"]", got)
	}
}

func TestFindInDirRequiresExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit test")
	}
	dir := t.TempDir()
	exec := filepath.Join(dir, "mytool")
	if err := os.WriteFile(exec, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notexec"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindInDir(dir, []string{"mytool", "notexec"}, env(), "linux")
	if len(got) != 1 || got[0] != exec {
		t.Fatalf("FindInDir = %v, want [%s]", got, exec)
	}
}

func TestClassifyOriginTable(t *testing.T) {
	cases := []struct{ path, want string }{
		{`C:\msys64\ucrt64\bin\gcc.exe`, "msys2"},
		{`C:\Program Files\LLVM\bin\clang.exe`, "llvm"},
		{`C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Tools\MSVC\14.3\bin\cl.exe`, "visual-studio"},
		{"/opt/homebrew/bin/go", "homebrew"},
		{"/usr/local/Cellar/node/21/bin/node", "homebrew"},
		{"/snap/bin/node", "snap"},
		{"/home/z/.pyenv/shims/python", "pyenv"},
		{`C:\Users\z\AppData\Local\Microsoft\WindowsApps\python.exe`, "WindowsApps"},
		{"/mnt/c/Users/z/scoop/shims/node.exe", "WSL"},
		{"/usr/bin/gcc", "system"},
	}
	for _, c := range cases {
		if got := ClassifyOrigin(c.path, "linux"); got != c.want {
			t.Errorf("ClassifyOrigin(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestLowPriorityOrigins(t *testing.T) {
	for _, o := range []string{"WSL", "WindowsApps"} {
		if !LowPriority[o] {
			t.Errorf("LowPriority[%q] = false, want true", o)
		}
	}
	if LowPriority["system"] {
		t.Error("LowPriority[system] = true, want false")
	}
}

func TestToWSLPath(t *testing.T) {
	if got := ToWSLPath(`C:\a\b.sh`); got != "/mnt/c/a/b.sh" {
		t.Errorf("ToWSLPath = %q, want /mnt/c/a/b.sh", got)
	}
	if got := ToWSLPath("/already/posix"); got != "/already/posix" {
		t.Errorf("ToWSLPath(posix) = %q", got)
	}
}
