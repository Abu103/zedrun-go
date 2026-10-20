package sources

import (
	"strings"
	"testing"
	"time"

	"github.com/zedrun/zedrun-go/internal/versions"
)

// The tools that had no version feed at all. Each provider below was checked
// against the project's own endpoint, and these fixtures are the shape of that
// endpoint's real response.

func TestGnuFTPBashIndex(t *testing.T) {
	fx := fixtures{
		"https://ftp.gnu.org/gnu/bash/": `
<html><body><pre>
<a href="bash-5.1.tar.gz">bash-5.1.tar.gz</a>
<a href="bash-5.2.tar.gz">bash-5.2.tar.gz</a>
<a href="bash-5.2.21.tar.gz">bash-5.2.21.tar.gz</a>
<a href="bash-5.2.37.tar.gz">bash-5.2.37.tar.gz</a>
<a href="bash-5.2.37.tar.gz.sig">bash-5.2.37.tar.gz.sig</a>
</pre></body></html>`,
	}
	p := Product{ID: "bash", Kind: "gnu-ftp", Params: map[string]string{"url": "https://ftp.gnu.org/gnu/bash/"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "5.2.37" {
		t.Errorf("Stable = %q, want 5.2.37 (source %q)", l.Stable, l.Source)
	}
}

// A GNU directory also holds patch files and diffs; none of them are releases.
func TestGnuFTPBashIgnoresDiffsAndSigs(t *testing.T) {
	fx := fixtures{
		"https://ftp.gnu.org/gnu/bash/": `
<a href="bash-5.1.16.tar.gz">bash-5.1.16.tar.gz</a>
<a href="bash-5.1.16-5.2.diff.gz">bash-5.1.16-5.2.diff.gz</a>
<a href="bash-5.1.tar.gz.sig">bash-5.1.tar.gz.sig</a>`,
	}
	p := Product{ID: "bash", Kind: "gnu-ftp", Params: map[string]string{"url": "https://ftp.gnu.org/gnu/bash/"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "5.1.16" {
		t.Errorf("Stable = %q, want 5.1.16", l.Stable)
	}
}

func TestPhpJSONIndex(t *testing.T) {
	fx := fixtures{
		"https://www.php.net/releases/index.php?json": `{
  "8": {"version": "8.5.11", "tags": ["security"]},
  "7": {"version": "7.4.33", "tags": ["security"]}
}`,
	}
	p := Product{ID: "php", Kind: "php-json", Params: map[string]string{"url": "https://www.php.net/releases/index.php?json"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "8.5.11" {
		t.Errorf("Stable = %q, want 8.5.11", l.Stable)
	}
}

// php.net lists the newest supported branch first; the highest version wins
// even if the JSON object order were reversed.
func TestPhpJSONPicksTheHighestBranch(t *testing.T) {
	fx := fixtures{
		"https://www.php.net/releases/index.php?json": `{
  "7": {"version": "7.4.33"},
  "8": {"version": "8.3.14"}
}`,
	}
	p := Product{ID: "php", Kind: "php-json", Params: map[string]string{"url": "https://www.php.net/releases/index.php?json"}}
	if l := FetchLatest(p, fetcherFor(t, fx, time.Hour)); l.Stable != "8.3.14" {
		t.Errorf("Stable = %q, want 8.3.14", l.Stable)
	}
}

// metacpan reports perl's version in the internal 5.044000 form, which would
// compare as 5.44.000 against an installed 5.38.2. The archive filename is the
// readable form, so it is what the provider uses.
func TestMetacpanPerlVersion(t *testing.T) {
	fx := fixtures{
		"https://fastapi.metacpan.org/v1/release/perl": `{
  "name": "perl",
  "version": "5.044000",
  "archive": "perl-5.44.0.tar.gz",
  "authorized": true
}`,
	}
	p := Product{ID: "perl", Kind: "metacpan", Params: map[string]string{"url": "https://fastapi.metacpan.org/v1/release/perl"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "5.44.0" {
		t.Errorf("Stable = %q, want 5.44.0 (source %q)", l.Stable, l.Source)
	}
	// And it must compare correctly against a real installed perl.
	state, _, _ := versions.Judge("5.38.2", l, "stable")
	if state != "outdated" {
		t.Errorf("perl 5.38.2 vs 5.44.0 judged %q, want outdated", state)
	}
}

func TestSqliteChangesPage(t *testing.T) {
	fx := fixtures{
		"https://sqlite.org/changes.html": `
<html><body>
<h2 id="version_3_54_1">Version 3.54.1</h2>
<h2 id="version_3_54_0">Version 3.54.0</h2>
<h2 id="version_3_53_4">Version 3.53.4</h2>
</body></html>`,
	}
	p := Product{ID: "sqlite3", Kind: "sqlite-changes", Params: map[string]string{"url": "https://sqlite.org/changes.html"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "3.54.1" {
		t.Errorf("Stable = %q, want 3.54.1 (source %q)", l.Stable, l.Source)
	}
}

func TestCranRIndex(t *testing.T) {
	fx := fixtures{
		"https://cran.r-project.org/src/base/": `<a href="R-3/">R-3/</a><a href="R-4/">R-4/</a>`,
		"https://cran.r-project.org/src/base/R-4/": `
<a href="R-4.4.1.tar.gz">R-4.4.1.tar.gz</a>
<a href="R-4.5.0.tar.gz">R-4.5.0.tar.gz</a>
<a href="R-4.5.1.tar.gz">R-4.5.1.tar.gz</a>`,
	}
	p := Product{ID: "r", Kind: "cran-ftp", Params: map[string]string{"url": "https://cran.r-project.org/src/base/"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "4.5.1" {
		t.Errorf("Stable = %q, want 4.5.1 (source %q)", l.Stable, l.Source)
	}
}

// GHC's gitlab tags include branch markers ("ghc-10.0-start") and rc/alpha
// builds. The markers are not releases and must not be reported as the newest
// stable, which would make every installed GHC look ancient.
func TestGhcGitlabTagsSkipBranchMarkers(t *testing.T) {
	fx := fixtures{
		"https://gitlab.haskell.org/ghc/ghc/-/tags?format=atom": `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>GHC tags</title>
  <entry><title>ghc-10.0.1-alpha1</title></entry>
  <entry><title>ghc-9.14.2-rc2</title></entry>
  <entry><title>ghc-10.0-start</title></entry>
  <entry><title>ghc-10.1-start</title></entry>
  <entry><title>ghc-9.14.1-release</title></entry>
  <entry><title>ghc-9.12.4</title></entry>
</feed>`,
	}
	p := Product{ID: "ghc", Kind: "gitlab-tags", Params: map[string]string{"url": "https://gitlab.haskell.org/ghc/ghc/-/tags?format=atom"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "9.14.1" {
		t.Errorf("Stable = %q, want 9.14.1 (source %q)", l.Stable, l.Source)
	}
}

// TypeScript tags its releases "TypeScript 7.0.2" and also carries
// vscode-typescript/v1.0.1, which is a different project's numbering.
func TestTypeScriptTitlesParse(t *testing.T) {
	fx := fixtures{
		"https://github.com/microsoft/TypeScript/releases.atom": `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>TypeScript 6.0.3</title></entry>
  <entry><title>TypeScript 7.0.2</title></entry>
  <entry><title>TypeScript 6.0 Beta</title></entry>
  <entry><title>TypeScript 5.9.3</title></entry>
</feed>`,
	}
	p := Product{ID: "tsc", Kind: "github-atom", Params: map[string]string{"repo": "microsoft/TypeScript"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "TypeScript 7.0.2" {
		t.Errorf("Stable = %q, want TypeScript 7.0.2", l.Stable)
	}
	if l.Pre != "TypeScript 6.0 Beta" {
		t.Errorf("Pre = %q, want TypeScript 6.0 Beta", l.Pre)
	}
}

func TestLuaAtom(t *testing.T) {
	fx := fixtures{
		"https://github.com/lua/lua/releases.atom": `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>v5.4.9</title></entry>
  <entry><title>Lua 5.5.1</title></entry>
  <entry><title>rc: In luaB_close, running coroutines do not go to default</title></entry>
</feed>`,
	}
	p := Product{ID: "lua", Kind: "github-atom", Params: map[string]string{"repo": "lua/lua"}}
	l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
	if l.Stable != "Lua 5.5.1" {
		t.Errorf("Stable = %q, want Lua 5.5.1", l.Stable)
	}
}

// A provider that returns nothing usable must say so, not invent a version.
func TestNewProvidersReportErrorsHonestly(t *testing.T) {
	fx := fixtures{
		"https://ftp.gnu.org/gnu/bash/": `<html><body><pre><a href="bash-5.2.tar.gz.sig">x</a></pre></body></html>`,
		"https://www.php.net/releases/index.php?json": `{}`,
		"https://fastapi.metacpan.org/v1/release/perl": `{"archive": ""}`,
		"https://sqlite.org/changes.html": `<html><body>nothing</body></html>`,
		"https://cran.r-project.org/src/base/": `<a href="R-0/">R-0/</a>`,
		"https://gitlab.haskell.org/ghc/ghc/-/tags?format=atom": `<feed></feed>`,
	}
	cases := []Product{
		{ID: "bash", Kind: "gnu-ftp", Params: map[string]string{"url": "https://ftp.gnu.org/gnu/bash/"}},
		{ID: "php", Kind: "php-json", Params: map[string]string{"url": "https://www.php.net/releases/index.php?json"}},
		{ID: "perl", Kind: "metacpan", Params: map[string]string{"url": "https://fastapi.metacpan.org/v1/release/perl"}},
		{ID: "sqlite3", Kind: "sqlite-changes", Params: map[string]string{"url": "https://sqlite.org/changes.html"}},
		{ID: "r", Kind: "cran-ftp", Params: map[string]string{"url": "https://cran.r-project.org/src/base/"}},
		{ID: "ghc", Kind: "gitlab-tags", Params: map[string]string{"url": "https://gitlab.haskell.org/ghc/ghc/-/tags?format=atom"}},
	}
	for _, p := range cases {
		l := FetchLatest(p, fetcherFor(t, fx, time.Hour))
		if !strings.HasPrefix(l.Source, "error") {
			t.Errorf("%s reported %q instead of an error", p.ID, l.Source)
		}
		if l.Stable != "" || l.Pre != "" {
			t.Errorf("%s invented a version from an unusable payload: %+v", p.ID, l)
		}
	}
}
