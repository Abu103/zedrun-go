package versions

import "testing"

// A version source often hands back more than a version. Printing it verbatim
// misaligned the outdated table and misreported what the newest version is:
// rust's channel manifest carries the commit and date, and a GitHub release
// title carries prose.
func TestExtractVersion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.99.1 (58cf8f9c3 2026-10-13)", "1.99.1"},
		{"v7.6.7 Release of PowerShell", "7.6.7"},
		{"go1.27.2", "1.27.2"},
		{"LLVM 23.1.3", "23.1.3"},
		{"v24.22.0", "24.22.0"},
		{"25", "25"},
		{"2.9.7", "2.9.7"},
		{"  3.14.7  ", "3.14.7"},
		{"build-2.5.20-dev-1642", "2.5.20-dev-1642"},
		{"1.99.0-rc.1 (abc123)", "1.99.0-rc.1"},
		{"", ""},
		{"no version here", ""},
		{"win32", "32"},
	} {
		if got := ExtractVersion(tc.in); got != tc.want {
			t.Errorf("ExtractVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ExtractVersion must not break the values that were already clean.
func TestExtractVersionLeavesCleanVersionsAlone(t *testing.T) {
	for _, s := range []string{"1.2.3", "24.22.0", "3.13.7", "7.6.7"} {
		if got := ExtractVersion(s); got != s {
			t.Errorf("ExtractVersion(%q) = %q, want it unchanged", s, got)
		}
	}
}
