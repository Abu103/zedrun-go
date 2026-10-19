package versions

import (
	"strings"
	"testing"
)

// The exact strings the providers hand back, driven through Judge — this is the
// path the outdated table takes, and it is where the column broke.
func TestJudgeReportsABareVersionForEveryProvider(t *testing.T) {
	for _, tc := range []struct{ name, installed, latest, wantState, wantTarget string }{
		{"rust channel manifest", "1.99.1", "1.99.1 (58cf8f9c3 2026-10-13)", "up-to-date", "1.99.1"},
		{"github release title", "7.6.6", "v7.6.7 Release of PowerShell", "outdated", "7.6.7"},
		{"go json", "1.26.3", "go1.27.2", "outdated", "1.27.2"},
		{"llvm tag", "23.1.3", "LLVM 23.1.3", "up-to-date", "23.1.3"},
		{"node json", "24.6.0", "v24.22.0", "outdated", "24.22.0"},
		{"deno tag", "2.9.7", "v2.9.7", "up-to-date", "2.9.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, target, _ := Judge(tc.installed, Latest{Stable: tc.latest, Source: "test"}, "stable")
			if target != tc.wantTarget {
				t.Errorf("target = %q, want %q", target, tc.wantTarget)
			}
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			// the table has a 12-column budget for this value
			if strings.ContainsAny(target, " \t") {
				t.Errorf("target %q still carries prose and will break the column", target)
			}
		})
	}
}

// A prefixed latest must still compare correctly against a bare installed
// version — the whole point is that only the presentation changes.
func TestJudgeComparesThroughPrefixesAndProse(t *testing.T) {
	state, target, bump := Judge("1.26.3", Latest{Stable: "go1.27.2", Source: "t"}, "stable")
	if state != "outdated" || bump != "minor" {
		t.Errorf("state = %q bump = %q, want outdated/minor", state, bump)
	}
	if target != "1.27.2" {
		t.Errorf("target = %q, want 1.27.2", target)
	}
}

// An alpha must not be reduced to its numeric part, or it reads as a final
// release and the tool tells you that you are out of date when you are not.
func TestJudgeKeepsAnAttachedPreReleaseSuffix(t *testing.T) {
	for _, tc := range []struct{ latest, want string }{
		{"3.15.0a1", "3.15.0a1"},
		{"1.24rc1", "1.24rc1"},
		{"3.14.0b2", "3.14.0b2"},
	} {
		_, target, _ := Judge("1.0.0", Latest{Stable: tc.latest, Source: "t"}, "stable")
		if target != tc.want {
			t.Errorf("target = %q, want %q", target, tc.want)
		}
	}
}

// Errors must keep their text so the reason still reaches the user.
func TestJudgeLeavesErrorSourcesAlone(t *testing.T) {
	state, target, _ := Judge("1.0.0", Latest{Source: "error: offline"}, "stable")
	if state != "unknown" || target != "" {
		t.Errorf("state = %q target = %q, want unknown/empty", state, target)
	}
}
