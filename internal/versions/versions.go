// Package versions parses and compares version strings the way toolchains
// actually emit them (go1.23rc1, v1.2.3-beta.1, bun-v1.1.30, 3.14.0a2,
// 21.0.2+13) and judges an installed version against the latest known one.
package versions

import (
	"strconv"
	"strings"
)

// Pre-release ranks, lowest first. A final release outranks every pre-release
// of the same numbers.
const (
	RankAlpha = 0
	RankBeta  = 1
	RankRC    = 2
	RankFinal = 3
)

// Version is a parsed version: numeric components plus pre-release rank/number.
type Version struct {
	Nums    []int
	PreRank int
	PreNum  int
	Raw     string
}

// Latest is what a provider reports about the newest release of a product.
// Stable is the ecosystem's recommended release (LTS for Node/Java), Current is
// the newest feature release where that differs, and Pre is the newest
// pre-release (beta/rc/preview/EA).
type Latest struct {
	Stable  string
	Pre     string
	Current string
	Source  string
}

// preWords map a pre-release token to its rank. Longer tokens are listed first
// so "preview" is read as one token rather than "pre".
var preWords = []struct {
	word string
	rank int
}{
	{"alpha", RankAlpha},
	{"nightly", RankAlpha},
	{"snapshot", RankAlpha},
	{"dev", RankAlpha},
	{"a", RankAlpha},
	{"beta", RankBeta},
	{"b", RankBeta},
	{"preview", RankRC},
	{"rc", RankRC},
	{"pre", RankRC},
	{"ea", RankRC},
}

// ParseVersion reads a version from an arbitrary toolchain banner value. It
// skips any leading non-digits (v, go, bun-v, rust-), reads the dotted numeric
// part, then classifies any trailing pre-release token.
func ParseVersion(s string) (Version, bool) {
	raw := strings.TrimSpace(s)
	i := 0
	for i < len(raw) && !isDigit(raw[i]) {
		i++
	}
	var nums []int
	for i < len(raw) {
		j := i
		for j < len(raw) && isDigit(raw[j]) {
			j++
		}
		n, err := strconv.Atoi(raw[i:j])
		if err != nil {
			return Version{}, false
		}
		nums = append(nums, n)
		i = j
		if i < len(raw) && raw[i] == '.' {
			i++
			continue
		}
		break
	}
	if len(nums) == 0 {
		return Version{}, false
	}
	rank, preNum := classifyPre(strings.ToLower(raw[i:]))
	return Version{Nums: nums, PreRank: rank, PreNum: preNum, Raw: raw}, true
}

// ParseStrictVersion reads a version from a string that has to BE a version,
// rather than merely contain one. Directory listings and release tags need it:
// the python.org FTP index lists "win32" beside "3.14.7", and ParseVersion
// cheerfully reads the 32 out of the former and then reports it as newer than
// every real release.
func ParseStrictVersion(s string) (Version, bool) {
	raw := strings.TrimSpace(s)
	if raw == "" || !isDigit(raw[0]) {
		return Version{}, false
	}
	return ParseVersion(raw)
}

func classifyPre(rest string) (int, int) {
	for _, pw := range preWords {
		if k := indexToken(rest, pw.word); k >= 0 {
			return pw.rank, firstInt(rest[k+len(pw.word):])
		}
	}
	return RankFinal, 0
}

// indexToken finds word in rest at a token boundary: the neighbouring
// characters must not be letters, so "a" does not match inside "stable".
func indexToken(rest, word string) int {
	for i := 0; i+len(word) <= len(rest); i++ {
		if rest[i:i+len(word)] != word {
			continue
		}
		if i > 0 && isLetter(rest[i-1]) {
			continue
		}
		if j := i + len(word); j < len(rest) && isLetter(rest[j]) {
			continue
		}
		return i
	}
	return -1
}

func firstInt(s string) int {
	i := 0
	for i < len(s) && !isDigit(s[i]) {
		i++
	}
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if i == j {
		return 0
	}
	n, _ := strconv.Atoi(s[i:j])
	return n
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// Compare returns -1, 0 or 1. Missing numeric components count as zero, so
// 1.2 and 1.2.0 are equal.
func Compare(a, b Version) int {
	n := max(len(a.Nums), len(b.Nums))
	for i := 0; i < n; i++ {
		x, y := component(a, i), component(b, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	if a.PreRank != b.PreRank {
		if a.PreRank < b.PreRank {
			return -1
		}
		return 1
	}
	if a.PreNum != b.PreNum {
		if a.PreNum < b.PreNum {
			return -1
		}
		return 1
	}
	return 0
}

func component(v Version, i int) int {
	if i < len(v.Nums) {
		return v.Nums[i]
	}
	return 0
}

// IsPrerelease reports whether v is not a final release.
func IsPrerelease(v Version) bool { return v.PreRank < RankFinal }

// Judge compares an installed version against the latest known versions.
// channel is "stable" or "pre". It returns the state, the version to upgrade to
// ("" when there is none) and the bump kind (major|minor|patch|"").
func Judge(installed string, l Latest, channel string) (state, target, bump string) {
	if strings.HasPrefix(l.Source, "error") {
		return "unknown", "", ""
	}
	target = l.Stable
	if channel == "pre" && l.Pre != "" {
		pv, pok := ParseVersion(l.Pre)
		sv, sok := ParseVersion(l.Stable)
		if pok && (!sok || Compare(pv, sv) > 0) {
			target = l.Pre
		}
	}
	if target == "" {
		return "no-source", "", ""
	}
	iv, ok := ParseVersion(installed)
	if !ok {
		return "unknown", "", ""
	}
	tv, ok := ParseVersion(target)
	if !ok {
		return "unknown", "", ""
	}
	switch c := Compare(iv, tv); {
	case c == 0:
		return "up-to-date", target, ""
	case c > 0:
		return "ahead", target, ""
	default:
		return "outdated", target, bumpKind(iv, tv)
	}
}

func bumpKind(iv, tv Version) string {
	switch {
	case component(tv, 0) != component(iv, 0):
		return "major"
	case component(tv, 1) != component(iv, 1):
		return "minor"
	default:
		return "patch"
	}
}
