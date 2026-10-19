package versions

import "testing"

// ParseVersion is deliberately lenient: it reads a version out of a toolchain
// banner like "gcc (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0", so it skips leading
// junk. That leniency is wrong for a directory listing or a release tag, where
// the whole string has to BE a version.
func TestParseStrictVersionRejectsNonVersionNames(t *testing.T) {
	for _, s := range []string{
		"win32", "win64", "amd64", "x86", "src", "docs", "osx", "arm64",
		"README", "", "   ",
	} {
		if v, ok := ParseStrictVersion(s); ok {
			t.Errorf("ParseStrictVersion(%q) accepted %+v as a version", s, v)
		}
	}
}

func TestParseStrictVersionAcceptsRealReleases(t *testing.T) {
	cases := map[string][]int{
		"3.14.7":    {3, 14, 7},
		"2.0":       {2, 0},
		"3.15.0a1":  {3, 15, 0},
		"3.14.0rc1": {3, 14, 0},
		"3.13.0b2":  {3, 13, 0},
		"1.0.0":     {1, 0, 0},
	}
	for s, want := range cases {
		v, ok := ParseStrictVersion(s)
		if !ok {
			t.Errorf("ParseStrictVersion(%q) rejected a real release", s)
			continue
		}
		if len(v.Nums) != len(want) {
			t.Errorf("ParseStrictVersion(%q).Nums = %v, want %v", s, v.Nums, want)
			continue
		}
		for i := range want {
			if v.Nums[i] != want[i] {
				t.Errorf("ParseStrictVersion(%q).Nums = %v, want %v", s, v.Nums, want)
				break
			}
		}
	}
}

// The bug this exists for: python.org/ftp/python/ lists a "win32" entry beside
// "3.14.7". ParseVersion reads the 32 out of "win32" and classifies it as a
// final release, and 32 > 3, so "win32" beat every real release and was
// reported as the newest stable Python.
func TestParseStrictVersionBeatsWin32(t *testing.T) {
	win, ok := ParseVersion("win32")
	if !ok {
		t.Skip("ParseVersion no longer reads win32, so the guard is moot")
	}
	if IsPrerelease(win) {
		t.Fatalf("sanity: ParseVersion(%q) = %+v, expected it to look like a final release", "win32", win)
	}
	real, _ := ParseStrictVersion("3.14.7")
	if Compare(real, win) > 0 {
		t.Fatalf("sanity: 3.14.7 should not outrank win32 numerically — that is why the " +
			"provider must reject win32 outright rather than compare it")
	}
	if _, ok := ParseStrictVersion("win32"); ok {
		t.Fatal("ParseStrictVersion accepted win32, so it does not guard this")
	}
}
