package provision

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/versions"
)

// UpdateRoute is what the update check can offer for one installed tool.
type UpdateRoute struct {
	Method     Method // zero when nothing on this machine can update it
	Achievable string // the version Method installs; "" when unknown
	Reason     string // why there is no command, when there is none
}

// NextVersion is the version this route would install, when that is a plausible
// next version of a tool whose installed version is installed.
//
// It is "" when the route has no version to report, when the version is not
// newer, or when it belongs to a different product. Git.Git's 2.55.0.5 is not
// bash's next version — it is Git's — and printing it as bash's would be a lie.
// go's 1.27.0 is go's next version, so it is printed.
func (r UpdateRoute) NextVersion(installed string) string {
	return nextVersion(installed, r.Achievable)
}

// nextVersion is NextVersion for a bare version, so a caller holding a version
// it got from elsewhere is held to the same rule.
func nextVersion(installed, achievable string) string {
	if achievable == "" || !versions.SameProduct(installed, achievable) {
		return ""
	}
	iv, ok := versions.ParseVersion(installed)
	if !ok {
		return ""
	}
	av, ok := versions.ParseVersion(achievable)
	if !ok || versions.Compare(av, iv) <= 0 {
		return ""
	}
	return achievable
}

// VersionProbe asks a manager what version it can install for a package, and
// how to read the answer.
//
// It exists because a manager's index can lag the upstream vendor: Ubuntu's
// golang-go is 1.22 while go.dev publishes 1.27.3, and `apt-get install
// --only-upgrade golang-go` on a machine that did not install it that way
// changes nothing at all. An update routed through such a manager must not
// promise a version it cannot deliver.
//
// winget does not use this: its package is resolved from `winget list` instead,
// because the table's id can be wrong and only the list knows what is really
// installed.
type VersionProbe struct {
	Argv []string                           // uses {package}
	Read func(stdout string) (string, bool) // version, ok
}

// probeCandidate runs a manager's version probe. The second result is false
// when the manager has no probe, is not on this machine, or the probe could not
// answer — a caller must then fall back to the vendor's own version rather than
// guessing.
func probeCandidate(m Manager, pkg string, env map[string]string) (string, bool) {
	if m.Candidate == nil || pkg == "" {
		return "", false
	}
	argv := substitute(m.Candidate.Argv, pkg)
	path, ok := LookPath(argv[0], env)
	if !ok {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	if len(env) > 0 {
		cmd.Env = envSlice(env)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return m.Candidate.Read(buf.String())
}

// aptCandidate reads the installable version out of `apt-cache policy`.
func aptCandidate(out string) (string, bool) {
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "Candidate:") {
			continue
		}
		v := aptClean(strings.TrimSpace(t[len("Candidate:"):]))
		if v == "" {
			return "", false
		}
		return v, true
	}
	return "", false
}

// aptClean reduces a Debian version to the part that names the upstream
// release. The epoch ("2:") and the Debian revision ("-1", "~2build1") are
// packaging details; comparing them against go.dev's 1.27.3 would report a
// difference that is not a version difference.
func aptClean(v string) string {
	if i := strings.Index(v, ":"); i > 0 && isDigits(v[:i]) {
		v = v[i+1:]
	}
	// A "~" sorts before the empty string in Debian, so "1.22~2build1" is 1.22
	// carrying a packaging suffix.
	if i := strings.Index(v, "~"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "-"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ResolveUpdate picks the manager that owns tool and checks that it can really
// update it here.
//
// A manager may update a tool only when it is the confirmed owner of the copy
// already installed. Confirmed means the install origin names it, or — for
// winget, whose index can be read — that `winget list` shows the package is
// here. Without that the manager's upgrade is really an install: a node put on
// the machine by the official installer was offered `choco upgrade -y
// nodejs-lts`, a package choco does not have either, which would have left a
// second node beside the first.
//
// The winget route is verified against winget itself. The package table names an
// id, but a machine can have a different package installed under the same tool —
// a table entry of Temurin.21 beside an installed Oracle.JDK.23, or NodeJS.LTS
// beside a node installed from the official installer. Running the command then
// installs a second copy beside the first, or a different tool entirely, so the
// route is dropped unless winget confirms the package is here.
//
// When winget does have the package but nothing newer, that is the answer rather
// than a reason to keep looking: this manager owns the copy, and falling through
// to scoop or choco would offer a command for a package that is not installed
// there either.
//
// origin is the install origin from platform.ClassifyOrigin, or "" when unknown.
// ix may be nil, which means no winget routes at all.
func ResolveUpdate(toolID, osName, installed, upstream, origin string, detected map[string]string, env map[string]string, ix *WingetIndex) UpdateRoute {
	owner := OwnerFor(origin)
	for _, method := range Order(Available(toolID, osName, detected, false), owner) {
		argv, ok := method.Update()
		if !ok {
			continue
		}
		if method.Manager == "winget" {
			pkg, found := wingetPackageFor(method.Package, installed, ix)
			if !found {
				// winget does not have this package, so it is not the owner.
				// No other manager can be confirmed either — an unknown origin
				// proves nothing — so there is no safe upgrade to offer.
				return UpdateRoute{Reason: notOwned(toolID, owner)}
			}
			// winget's answer may name a different package than the table does,
			// so the command has to be built from winget's id — otherwise it
			// upgrades a package that is not installed here.
			method.Package = pkg.ID
			if pkg.Available == "" {
				return UpdateRoute{Reason: managerStale(toolID, "winget", method.Package, pkg.Installed, installed, upstream)}
			}
			argv, ok := method.Update()
			if !ok {
				continue
			}
			method.Argv = argv
			return UpdateRoute{Method: method, Achievable: pkg.Available}
		}
		// A manager that did not install the tool must not be asked to upgrade
		// it. Its upgrade is an install: a second copy beside the first.
		if owner != "" && method.Manager != owner {
			continue
		}
		// A manager whose index is readable is asked what it can actually
		// deliver. Ubuntu's golang-go is 1.22 while go.dev has 1.27.3, and a
		// command that cannot reach the version installed is not an update.
		if mgr, ok := managerNamed(method.Manager); ok {
			if v, ok := probeCandidate(mgr, method.Package, env); ok {
				if nextVersion(installed, v) == "" {
					return UpdateRoute{Reason: managerStale(toolID, method.Manager, method.Package, v, installed, upstream)}
				}
				method.Argv = argv
				return UpdateRoute{Method: method, Achievable: v}
			}
		}
		method.Argv = argv
		return UpdateRoute{Method: method}
	}
	return UpdateRoute{Reason: notOwned(toolID, owner)}
}

// notOwned is the reason when nothing on this machine can be confirmed as the
// manager of a tool. It says why there is no command rather than staying silent,
// because a silent row reads as a bug.
//
// The two shapes differ in what is actually known. An origin that names a
// manager which then denies having the package is a stale origin, and saying so
// is more use than a generic apology. An unknown origin is simply unknown.
func notOwned(toolID, owner string) string {
	if owner != "" {
		return owner + " does not have " + toolID + " installed; upgrading through it would install a second copy beside the one here"
	}
	return "no package manager on this machine owns " + toolID +
		"; upgrading through one would install a second copy beside it"
}

// managerStale explains that a manager's index holds a version that is not a
// newer one for this tool. The wording follows the shape so the report says what
// is actually true: a package older than what is installed, one that matches it,
// or one that is a different product's version entirely.
func managerStale(tool, mgrName, pkg, have, installed, upstream string) string {
	iv, iok := versions.ParseVersion(installed)
	hv, hok := versions.ParseVersion(have)
	if !iok || !hok {
		return mgrName + "'s " + pkg + " is " + have + "; upstream " + tool + " is " + upstream
	}
	// A version from a different product is not a version of this tool at all.
	// Git.Git's 2.55.0.5 is Git's version, and putting it beside bash's 5.2.21
	// would produce a comparison that means nothing.
	if !versions.SameProduct(installed, have) {
		return mgrName + "'s " + pkg + " is " + have + ", which is not a newer " + tool +
			" (upstream " + tool + " " + upstream + ")"
	}
	switch versions.Compare(hv, iv) {
	case -1:
		return mgrName + "'s " + pkg + " is " + have + ", older than the " + installed +
			" installed here (upstream " + tool + " " + upstream + ")"
	case 0:
		return mgrName + "'s " + pkg + " has nothing newer than " + have + " (upstream " + tool + " " + upstream + ")"
	default:
		return mgrName + "'s " + pkg + " is " + have + ", which is not a newer " + tool +
			" (upstream " + tool + " " + upstream + ")"
	}
}

// wingetPackageFor resolves the winget package that owns a tool: the table's id
// when winget has it, otherwise the one installed package whose version matches.
func wingetPackageFor(id, installed string, ix *WingetIndex) (WingetPkg, bool) {
	if ix == nil {
		return WingetPkg{}, false
	}
	if p, ok := ix.Lookup(id); ok {
		return p, true
	}
	return ix.MatchVersion(installed)
}
