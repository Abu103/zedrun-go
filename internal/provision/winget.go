package provision

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zedrun/zedrun-go/internal/platform"
)

// WingetPkg is one installed winget package, as `winget list` reports it.
type WingetPkg struct {
	ID        string
	Name      string
	Installed string
	Available string // empty when winget has nothing newer
}

// WingetIndex is one `winget list` result, shared by every tool in a pass so a
// check that touches ten winget packages costs one call rather than ten.
//
// It exists because `winget upgrade` has no dry run: it installs. The only safe
// way to ask what winget would do is to list what it already has.
type WingetIndex struct{ rows []map[string]string }

// LoadWingetIndex asks winget what is installed. It returns nil when winget is
// not here or will not answer, which every caller treats as "no winget routes".
func LoadWingetIndex(env map[string]string, osName string) *WingetIndex {
	cmd := exec.Command(wingetExe(env, osName), "list")
	if len(env) > 0 {
		cmd.Env = envSlice(env)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return &WingetIndex{rows: wingetTable(string(out))}
}

// WingetIndexFunc memoises LoadWingetIndex so a pass that resolves several tools
// still costs one call. The returned function is not safe for concurrent use;
// a pass resolves tools one at a time.
func WingetIndexFunc(env map[string]string, osName string) func() *WingetIndex {
	var ix *WingetIndex
	var done bool
	return func() *WingetIndex {
		if !done {
			ix = LoadWingetIndex(env, osName)
			done = true
		}
		return ix
	}
}

// wingetExe resolves the winget executable. LookPath is not enough: it does not
// add the Windows executable extensions, so on a machine where winget is
// winget.exe it finds nothing and the index comes back empty — which reads as
// "no winget routes" rather than as a failed lookup.
func wingetExe(env map[string]string, osName string) string {
	if p, ok := LookPath("winget", env); ok {
		return p
	}
	for _, dir := range filepath.SplitList(platform.Env(env, osName, "PATH")) {
		if dir == "" {
			continue
		}
		for _, p := range platform.FindInDirCached(dir, []string{"winget"}, env, osName, nil) {
			return p
		}
	}
	return "winget"
}

// Lookup finds one package by id.
func (ix *WingetIndex) Lookup(id string) (WingetPkg, bool) {
	if ix == nil {
		return WingetPkg{}, false
	}
	for _, r := range ix.rows {
		if r["Id"] == id {
			return wingetPkg(r), true
		}
	}
	return WingetPkg{}, false
}

// MatchVersion finds the single winget package whose installed version matches.
//
// More than one match returns false: guessing between two packages is worse than
// saying nothing, and the table's id is only a guess about what this machine
// happens to have installed.
func (ix *WingetIndex) MatchVersion(v string) (WingetPkg, bool) {
	if ix == nil || v == "" {
		return WingetPkg{}, false
	}
	var found []WingetPkg
	for _, r := range ix.rows {
		if r["Source"] != "winget" {
			continue
		}
		if VersionMatches(r["Version"], v) {
			found = append(found, wingetPkg(r))
		}
	}
	if len(found) != 1 {
		return WingetPkg{}, false
	}
	return found[0], true
}

func wingetPkg(r map[string]string) WingetPkg {
	return WingetPkg{ID: r["Id"], Name: r["Name"], Installed: r["Version"], Available: r["Available"]}
}

// wingetTable parses `winget list` output into rows keyed by column name.
//
// winget pads its columns to fixed widths, so the header line gives the column
// boundaries and each row is sliced at those offsets. Splitting on whitespace
// instead is wrong twice over: an empty Available column collapses, and a
// package name containing two spaces shifts every column after it.
func wingetTable(out string) []map[string]string {
	lines := strings.Split(out, "\n")
	header := -1
	for i, ln := range lines {
		t := strings.TrimRight(ln, "\r")
		if strings.Contains(t, "Version") && strings.Contains(t, "Id") {
			header = i
			break
		}
	}
	if header < 0 {
		return nil
	}
	cols := wingetColumns(strings.TrimRight(lines[header], "\r"))
	if len(cols) == 0 {
		return nil
	}
	var rows []map[string]string
	for _, ln := range lines[header+1:] {
		t := strings.TrimRight(ln, "\r")
		if strings.TrimSpace(t) == "" || strings.Trim(t, "- ") == "" {
			continue
		}
		row := map[string]string{}
		for i, c := range cols {
			end := len(t)
			if i+1 < len(cols) {
				end = cols[i+1].start
			}
			switch {
			case c.start >= len(t):
				row[c.name] = ""
			case end > len(t):
				row[c.name] = strings.TrimSpace(t[c.start:])
			default:
				row[c.name] = strings.TrimSpace(t[c.start:end])
			}
		}
		// A warning line ("No installed package found matching input
		// criteria.") would otherwise parse as a row. A real row has an id and
		// a version.
		if row["Id"] == "" || !strings.ContainsAny(row["Version"], "0123456789") {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

type wingetColumn struct {
	name  string
	start int
}

// wingetColumns returns each header word and the offset it starts at.
func wingetColumns(header string) []wingetColumn {
	var cols []wingetColumn
	for i := 0; i < len(header); {
		for i < len(header) && header[i] == ' ' {
			i++
		}
		if i >= len(header) {
			break
		}
		j := i
		for j < len(header) && header[j] != ' ' {
			j++
		}
		cols = append(cols, wingetColumn{name: header[i:j], start: i})
		i = j
	}
	return cols
}

// VersionMatches reports whether two version strings agree on their leading
// components. winget writes 23.0.2.0 where a tool reports 23.0.2, and may prefix
// a bound with > or <, so an exact comparison would miss.
//
// The callers also use it to decide whether an achievable version belongs to the
// tool at all: Git.Git's 2.55.0.5 is not bash's next version, and printing it as
// one would be a lie.
func VersionMatches(have, want string) bool {
	h := versionCore(have)
	w := versionCore(want)
	if h == "" || w == "" {
		return false
	}
	hc := strings.Split(h, ".")
	wc := strings.Split(w, ".")
	n := len(wc)
	if len(hc) < n {
		n = len(hc)
	}
	for i := 0; i < n; i++ {
		if hc[i] != wc[i] {
			return false
		}
	}
	return true
}

func versionCore(v string) string {
	return strings.TrimLeft(strings.TrimSpace(v), "><vV= ")
}
