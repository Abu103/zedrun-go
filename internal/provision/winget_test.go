package provision

import (
	"strings"
	"testing"
)

type wingetRow struct{ name, id, version, available, source string }

// renderWinget builds a `winget list` table with the columns padded the way
// winget pads them, so the parser is exercised against the real layout rather
// than a hand-typed string that might not line up.
func renderWinget(rows ...wingetRow) string {
	head := []string{"Name", "Id", "Version", "Available", "Source"}
	const src = "winget"
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = len(h)
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		source := r.source
		if source == "" {
			source = src
		}
		c := []string{r.name, r.id, r.version, r.available, source}
		cells = append(cells, c)
		for i, v := range c {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}
	var b strings.Builder
	line := func(c []string) {
		for i, v := range c {
			if i == len(c)-1 {
				b.WriteString(v)
				break
			}
			b.WriteString(v)
			b.WriteString(strings.Repeat(" ", widths[i]-len(v)+3))
		}
		b.WriteString("\n")
	}
	line(head)
	total := 0
	for i, w := range widths {
		if i < len(widths)-1 {
			total += w + 3
		} else {
			total += w
		}
	}
	b.WriteString(strings.Repeat("-", total) + "\n")
	for _, c := range cells {
		line(c)
	}
	return b.String()
}

func ix(rows ...wingetRow) *WingetIndex {
	return &WingetIndex{rows: wingetTable(renderWinget(rows...))}
}

// The empty Available column is the case that breaks a whitespace split: it
// collapses, and every column after it shifts left.
func TestWingetTableKeepsAnEmptyAvailableColumn(t *testing.T) {
	got := ix(
		wingetRow{name: "PowerShell 7-x64", id: "Microsoft.PowerShell", version: "7.6.6.0", available: "7.6.7.0"},
		wingetRow{name: "Go Programming Language amd64 go1.27.0", id: "GoLang.Go", version: "1.27.0", available: ""},
		wingetRow{name: "Java(TM) SE Development Kit 23.0.2", id: "Oracle.JDK.23", version: "23.0.2.0", available: ""},
		wingetRow{name: "Android SDK Platform-Tools", id: "Google.PlatformTools", version: "37.0.1", available: ""},
	)
	pw, ok := got.Lookup("Microsoft.PowerShell")
	if !ok {
		t.Fatal("PowerShell row was not parsed")
	}
	if pw.Available != "7.6.7.0" {
		t.Errorf("Available = %q, want 7.6.7.0", pw.Available)
	}
	go_, ok := got.Lookup("GoLang.Go")
	if !ok {
		t.Fatal("Go row was not parsed")
	}
	if go_.Available != "" {
		t.Errorf("Available = %q, want empty", go_.Available)
	}
	if go_.Installed != "1.27.0" {
		t.Errorf("Installed = %q, want 1.27.0 — the empty Available column shifted it", go_.Installed)
	}
	// A name containing spaces must not shift the columns after it.
	if _, ok := got.Lookup("Google.PlatformTools"); !ok {
		t.Error("a package name with spaces was not parsed")
	}
}

// `winget list --id <unknown>` answers with a sentence, not a table. That must
// not be parsed as a row.
func TestWingetTableIgnoresTheNotFoundWarning(t *testing.T) {
	if rows := wingetTable("No installed package found matching input criteria.\n"); len(rows) != 0 {
		t.Fatalf("parsed %d rows from a warning line", len(rows))
	}
}

// A header without an Available column is a real shape: `winget list --id` omits
// it when nothing is installed, and the parser must not invent one.
func TestWingetTableWithoutAnAvailableColumn(t *testing.T) {
	out := "Name   Id        Version Source\n" +
		"------ --------- ------- ------\n" +
		"Go     GoLang.Go 1.27.0  winget\n"
	rows := wingetTable(out)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0]["Available"] != "" {
		t.Errorf("Available = %q, want empty", rows[0]["Available"])
	}
	if rows[0]["Version"] != "1.27.0" {
		t.Errorf("Version = %q, want 1.27.0", rows[0]["Version"])
	}
}

func TestWingetLookup(t *testing.T) {
	g := ix(wingetRow{name: "Go", id: "GoLang.Go", version: "1.27.0", available: "1.27.3"})
	if _, ok := g.Lookup("GoLang.Go"); !ok {
		t.Fatal("an installed package was not found")
	}
	if _, ok := g.Lookup("SQLite.SQLite"); ok {
		t.Fatal("a package that is not installed was found")
	}
	var nilIndex *WingetIndex
	if _, ok := nilIndex.Lookup("GoLang.Go"); ok {
		t.Fatal("a nil index answered a lookup")
	}
}

// The fallback matches on version, so a machine holding a different package
// under the same tool still resolves. It refuses to guess when two packages
// match.
func TestWingetMatchVersion(t *testing.T) {
	g := ix(
		wingetRow{name: "Go", id: "GoLang.Go", version: "1.27.0", available: ""},
		wingetRow{name: "Java", id: "Oracle.JDK.23", version: "23.0.2.0", available: "23.0.4.0"},
	)
	if p, ok := g.MatchVersion("23.0.2"); !ok || p.ID != "Oracle.JDK.23" {
		t.Fatalf("23.0.2 did not resolve to Oracle.JDK.23: %+v %v", p, ok)
	}
	if _, ok := g.MatchVersion("9.9.9"); ok {
		t.Fatal("an unknown version resolved")
	}
	// winget writes a bound for a pinned package; the comparison must see past it.
	if p, ok := ix(wingetRow{name: "Runtimes", id: "Microsoft.WindowsAppRuntime.1.8", version: "> 1.8.10", available: ""}).MatchVersion("1.8.10"); !ok || p.ID != "Microsoft.WindowsAppRuntime.1.8" {
		t.Fatalf("a > bound did not match: %+v %v", p, ok)
	}
	// Two packages at the same version: guessing between them is worse than
	// saying nothing.
	amb := ix(
		wingetRow{name: "A", id: "Vendor.A", version: "1.0.0", available: ""},
		wingetRow{name: "B", id: "Vendor.B", version: "1.0.0", available: ""},
	)
	if _, ok := amb.MatchVersion("1.0.0"); ok {
		t.Fatal("an ambiguous match resolved to one package")
	}
	// Only winget-sourced packages count: an ARP install is not winget's to
	// upgrade.
	mixed := ix(wingetRow{name: "Node", id: "ARP\\Machine\\X64\\Node", version: "24.6.0", available: "", source: "ARP"})
	if _, ok := mixed.MatchVersion("24.6.0"); ok {
		t.Fatal("a non-winget install matched")
	}
}

func TestVersionMatches(t *testing.T) {
	cases := []struct {
		have, want string
		wantMatch  bool
	}{
		{"23.0.2.0", "23.0.2", true},  // winget pads a component
		{"1.27.0", "1.27.0", true},    // identical
		{"> 1.8.10", "1.8.10", true},  // a bound prefix
		{"1.27.0", "1.27.3", false},   // a different patch
		{"2.55.0.5", "5.2.21", false}, // a different product's version
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		if got := VersionMatches(c.have, c.want); got != c.wantMatch {
			t.Errorf("VersionMatches(%q, %q) = %v, want %v", c.have, c.want, got, c.wantMatch)
		}
	}
}
