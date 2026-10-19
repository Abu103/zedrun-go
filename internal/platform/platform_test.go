package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// WSL does not always mount Windows drives at /mnt. /etc/wsl.conf can move the
// root or disable it entirely, and on such a distro the hardcoded /mnt prefix
// made every Windows path look like an ordinary system path: Windows shims
// stopped being low-priority (so python.exe could shadow the real python) and
// ToWSLPath handed shell scripts a path that did not exist.
func TestParseWSLMountRoot(t *testing.T) {
	cases := []struct{ name, conf, want string }{
		{"no automount section keeps the default", "[boot]\nsystemd=true\n", "/mnt"},
		{"empty config", "", "/mnt"},
		{"explicit root", "[automount]\nroot = /windir\n", "/windir"},
		{"trailing slash trimmed", "[automount]\nroot = /windir/\n", "/windir"},
		{"quoted root", "[automount]\nroot = \"/drives\"\n", "/drives"},
		{"spaces around the value", "[automount]\nroot   =   /d\n", "/d"},
		{"no spaces at all", "[automount]\nroot=/x\n", "/x"},
		{"keys are case insensitive", "[automount]\nRoot = /z\n", "/z"},
		{"disabled means no windows drives", "[automount]\nenabled = false\n", ""},
		{"disabled beats an explicit root", "[automount]\nroot = /windir\nenabled = false\n", ""},
		{"enabled true keeps the root", "[automount]\nroot = /d\nenabled = true\n", "/d"},
		{"comments and blank lines", "# hi\n[automount]\n\n# c\nroot=/y\n", "/y"},
		{"root from another section is ignored", "[network]\nroot = /nope\n[automount]\nroot = /real\n", "/real"},
		{"later section ends the automount block", "[automount]\nroot = /a\n[network]\nroot = /b\n", "/a"},
		{"section name is case insensitive", "[AutoMount]\nroot = /q\n", "/q"},
	}
	for _, c := range cases {
		if got := ParseWSLMountRoot(c.conf); got != c.want {
			t.Errorf("%s: ParseWSLMountRoot(%q) = %q, want %q", c.name, c.conf, got, c.want)
		}
	}
}

func TestToWSLPathAt(t *testing.T) {
	cases := []struct{ name, in, root, want string }{
		{"default root", `C:\a\b.sh`, "/mnt", "/mnt/c/a/b.sh"},
		{"custom root", `C:\a\b.sh`, "/windir", "/windir/c/a/b.sh"},
		{"nested custom root", `D:\x`, "/run/wsl/drives", "/run/wsl/drives/d/x"},
		{"drive letter lowercased", `C:\A\B`, "/mnt", "/mnt/c/A/B"},
		{"already posix is untouched", "/already/posix", "/mnt", "/already/posix"},
		{"no drives reachable leaves a slash path", `C:\a\b.sh`, "", "C:/a/b.sh"},
		{"bare drive", `C:`, "/mnt", "/mnt/c"},
	}
	for _, c := range cases {
		if got := ToWSLPathAt(c.in, c.root); got != c.want {
			t.Errorf("%s: ToWSLPathAt(%q, %q) = %q, want %q", c.name, c.in, c.root, got, c.want)
		}
	}
}

func TestIsWSLPathAt(t *testing.T) {
	cases := []struct {
		name, path, root string
		want             bool
	}{
		{"default mount", "/mnt/c/Users/z/scoop/shims/node.exe", "/mnt", true},
		{"custom mount", "/windir/c/Users/z/python.exe", "/windir", true},
		{"custom mount against the default root", "/windir/c/Users/z/python.exe", "/mnt", false},
		{"nested mount", "/run/wsl/drives/c/x.exe", "/run/wsl/drives", true},
		{"no drive letter", "/mnt/1/x", "/mnt", false},
		{"mount point itself", "/mnt/c", "/mnt", false},
		{"no trailing segment", "/mnt/", "/mnt", false},
		{"ordinary linux path", "/usr/bin/go", "/mnt", false},
		{"empty", "", "/mnt", false},
		{"drives unreachable", "/mnt/c/x", "", false},
	}
	for _, c := range cases {
		if got := isWSLPathAt(strings.ToLower(c.path), c.root); got != c.want {
			t.Errorf("%s: isWSLPathAt(%q, %q) = %v, want %v", c.name, c.path, c.root, got, c.want)
		}
	}
}

func TestKernelIsWSL(t *testing.T) {
	wsl := "Linux version 6.18.40.1-microsoft-standard-WSL2 (root@x) #1 SMP"
	if !KernelIsWSL(wsl) {
		t.Error("a WSL2 kernel string was not recognised")
	}
	if !KernelIsWSL("Linux version 4.4.0-19041-Microsoft (Microsoft@Microsoft.com)") {
		t.Error("a WSL1 kernel string was not recognised")
	}
	for _, plain := range []string{
		"",
		"Linux version 6.8.0-45-generic (buildd@lcy02) #45-Ubuntu SMP",
		"Darwin Kernel Version 24.0.0",
	} {
		if KernelIsWSL(plain) {
			t.Errorf("non-WSL kernel %q was misdetected", plain)
		}
	}
}

func TestIsWSLFromEnvMarkers(t *testing.T) {
	marker := env("WSL_DISTRO_NAME", "Ubuntu-24.04", "WSL_INTEROP", "/run/WSL/1_interop")
	if !wslEnvMarker(marker, "linux") {
		t.Error("WSL_DISTRO_NAME/WSL_INTEROP were not treated as a WSL marker")
	}
	if !wslEnvMarker(env("WSL_DISTRO_NAME", "Alpine"), "linux") {
		t.Error("WSL_DISTRO_NAME alone was not treated as a WSL marker")
	}
	// wslEnvMarker is the part that must be decided by the environment alone.
	// IsWSL also consults /proc/version, so it legitimately returns true for a
	// plain environment when the tests are running on a WSL host.
	if wslEnvMarker(env("PATH", "/usr/bin"), "linux") {
		t.Error("a plain linux environment was reported as a WSL marker")
	}
	if wslEnvMarker(env("WSL_DISTRO_NAME", "Ubuntu"), "windows") {
		t.Error("WSL markers must not apply when the OS override says windows")
	}
	if IsWSL(env("WSL_DISTRO_NAME", "Ubuntu"), "windows") {
		t.Error("IsWSL must be false for a windows target even with WSL markers set")
	}
	if !IsWSL(marker, "linux") {
		t.Error("IsWSL did not honour the environment markers")
	}
}

func TestToWSLPathFollowsTheDetectedRoot(t *testing.T) {
	root := WSLMountRoot()
	if root == "" {
		t.Skip("this distro has Windows drives disabled, so there is nothing to convert")
	}
	if got, want := ToWSLPath(`C:\a\b.sh`), root+"/c/a/b.sh"; got != want {
		t.Errorf("ToWSLPath = %q, want %q (detected root %q)", got, want, root)
	}
}

func TestWSLSummary(t *testing.T) {
	if got := WSLSummary(env("PATH", "/usr/bin"), "windows"); got != "" {
		t.Errorf("WSLSummary on a windows target = %q, want empty", got)
	}
	got := WSLSummary(env("WSL_DISTRO_NAME", "Alpine"), "linux")
	if got == "" {
		t.Fatal("WSLSummary reported nothing for a WSL distro")
	}
	if !strings.Contains(got, "Alpine") {
		t.Errorf("WSLSummary = %q, want the distro name in it", got)
	}
	if root := WSLMountRoot(); root != "" && !strings.Contains(got, root) {
		t.Errorf("WSLSummary = %q, want the mount root %q in it", got, root)
	}
}
