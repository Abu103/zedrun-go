package versions

import (
	"reflect"
	"testing"
)

func TestParseVariants(t *testing.T) {
	cases := []struct {
		in   string
		nums []int
		rank int
		pre  int
	}{
		{"go1.23rc1", []int{1, 23}, RankRC, 1},
		{"v1.2.3-beta.1", []int{1, 2, 3}, RankBeta, 1},
		{"bun-v1.1.30", []int{1, 1, 30}, RankFinal, 0},
		{"3.14.0a2", []int{3, 14, 0}, RankAlpha, 2},
		{"21.0.2+13", []int{21, 0, 2}, RankFinal, 0},
		{"13.3.0", []int{13, 3, 0}, RankFinal, 0},
		{"1.2.3", []int{1, 2, 3}, RankFinal, 0},
	}
	for _, c := range cases {
		v, ok := ParseVersion(c.in)
		if !ok {
			t.Fatalf("ParseVersion(%q) failed", c.in)
		}
		if !reflect.DeepEqual(v.Nums, c.nums) {
			t.Errorf("%q nums = %v, want %v", c.in, v.Nums, c.nums)
		}
		if v.PreRank != c.rank {
			t.Errorf("%q rank = %d, want %d", c.in, v.PreRank, c.rank)
		}
		if v.PreNum != c.pre {
			t.Errorf("%q preNum = %d, want %d", c.in, v.PreNum, c.pre)
		}
	}
}

func TestParseRejectsNonVersion(t *testing.T) {
	if _, ok := ParseVersion("not-a-version"); ok {
		t.Fatal("ParseVersion(not-a-version) = ok, want failure")
	}
	if _, ok := ParseVersion(""); ok {
		t.Fatal("ParseVersion(\"\") = ok, want failure")
	}
}

func TestStableIsNotMistakenForAlpha(t *testing.T) {
	// "stable" contains the letter "a"; the token scan must not read it as alpha.
	v, ok := ParseVersion("1.0.0-stable")
	if !ok {
		t.Fatal("ParseVersion(1.0.0-stable) failed")
	}
	if v.PreRank != RankFinal {
		t.Fatalf("rank = %d, want RankFinal", v.PreRank)
	}
}

func TestOrderingFinalBeatsRcBeatsBetaBeatsAlpha(t *testing.T) {
	order := []string{"1.0.0-alpha.1", "1.0.0-beta.1", "1.0.0-rc.1", "1.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := ParseVersion(order[i-1])
		b, _ := ParseVersion(order[i])
		if c := Compare(a, b); c >= 0 {
			t.Errorf("Compare(%q, %q) = %d, want < 0", order[i-1], order[i], c)
		}
	}
}

func TestTrailingZerosEqual(t *testing.T) {
	a, _ := ParseVersion("1.2")
	b, _ := ParseVersion("1.2.0")
	if c := Compare(a, b); c != 0 {
		t.Fatalf("Compare(1.2, 1.2.0) = %d, want 0", c)
	}
}

func TestIsPrerelease(t *testing.T) {
	for _, s := range []string{"1.0.0-rc.1", "1.0.0-beta", "1.0.0-alpha"} {
		v, _ := ParseVersion(s)
		if !IsPrerelease(v) {
			t.Errorf("IsPrerelease(%q) = false, want true", s)
		}
	}
	v, _ := ParseVersion("1.0.0")
	if IsPrerelease(v) {
		t.Error("IsPrerelease(1.0.0) = true, want false")
	}
}

func TestJudgeOutdatedMinor(t *testing.T) {
	state, target, bump := Judge("1.2.0", Latest{Stable: "1.3.4", Source: "test"}, "stable")
	if state != "outdated" || target != "1.3.4" || bump != "minor" {
		t.Fatalf("got (%q,%q,%q), want (outdated,1.3.4,minor)", state, target, bump)
	}
}

func TestJudgeOutdatedMajor(t *testing.T) {
	state, _, bump := Judge("13.3.0", Latest{Stable: "16.2.0", Source: "test"}, "stable")
	if state != "outdated" || bump != "major" {
		t.Fatalf("got (%q,%q), want (outdated,major)", state, bump)
	}
}

func TestJudgeOutdatedPatch(t *testing.T) {
	state, _, bump := Judge("1.2.3", Latest{Stable: "1.2.4", Source: "test"}, "stable")
	if state != "outdated" || bump != "patch" {
		t.Fatalf("got (%q,%q), want (outdated,patch)", state, bump)
	}
}

func TestJudgeUpToDate(t *testing.T) {
	state, _, bump := Judge("1.2.3", Latest{Stable: "1.2.3", Source: "test"}, "stable")
	if state != "up-to-date" || bump != "" {
		t.Fatalf("got (%q,%q), want (up-to-date,\"\")", state, bump)
	}
}

func TestJudgeAhead(t *testing.T) {
	if state, _, _ := Judge("2.0.0", Latest{Stable: "1.0.0", Source: "test"}, "stable"); state != "ahead" {
		t.Fatalf("state = %q, want ahead", state)
	}
}

func TestJudgePreChannelPrefersNewerPre(t *testing.T) {
	l := Latest{Stable: "1.0.0", Pre: "1.1.0-beta.1", Source: "test"}
	state, target, _ := Judge("1.0.0", l, "pre")
	if state != "outdated" || target != "1.1.0-beta.1" {
		t.Fatalf("pre channel: got (%q,%q), want (outdated,1.1.0-beta.1)", state, target)
	}
	state, target, _ = Judge("1.0.0", l, "stable")
	if state != "up-to-date" || target != "1.0.0" {
		t.Fatalf("stable channel: got (%q,%q), want (up-to-date,1.0.0)", state, target)
	}
}

func TestJudgePreChannelKeepsStableWhenNewer(t *testing.T) {
	l := Latest{Stable: "2.0.0", Pre: "1.9.0-rc.1", Source: "test"}
	state, target, _ := Judge("1.0.0", l, "pre")
	if state != "outdated" || target != "2.0.0" {
		t.Fatalf("got (%q,%q), want (outdated,2.0.0)", state, target)
	}
}

func TestJudgeInstalledRCVsFinal(t *testing.T) {
	state, _, _ := Judge("1.2.3-rc.1", Latest{Stable: "1.2.3", Source: "test"}, "stable")
	if state != "outdated" {
		t.Fatalf("state = %q, want outdated", state)
	}
}

func TestJudgeUnknownWhenUnparseable(t *testing.T) {
	if state, _, _ := Judge("not-a-version", Latest{Stable: "1.0.0", Source: "test"}, "stable"); state != "unknown" {
		t.Fatalf("state = %q, want unknown", state)
	}
}

func TestJudgeUnknownOnProviderError(t *testing.T) {
	if state, _, _ := Judge("1.0.0", Latest{Source: "error: timeout"}, "stable"); state != "unknown" {
		t.Fatalf("state = %q, want unknown", state)
	}
}

func TestJudgeNoSource(t *testing.T) {
	if state, _, _ := Judge("1.0.0", Latest{}, "stable"); state != "no-source" {
		t.Fatalf("state = %q, want no-source", state)
	}
}
