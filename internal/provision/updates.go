package provision

import (
	"fmt"

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
	if r.Achievable == "" || !versions.SameProduct(installed, r.Achievable) {
		return ""
	}
	iv, ok := versions.ParseVersion(installed)
	if !ok {
		return ""
	}
	av, ok := versions.ParseVersion(r.Achievable)
	if !ok || versions.Compare(av, iv) <= 0 {
		return ""
	}
	return r.Achievable
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
				return UpdateRoute{Reason: wingetStale(toolID, method.Package, pkg.Installed, upstream)}
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

// wingetStale explains that winget has the package but nothing newer than the
// version it reports. The two shapes are worth telling apart: a package that is
// simply behind upstream (GoLang.Go at 1.27.0 while go.dev has 1.27.3) versus a
// package that is not the tool at all (Git.Git's 2.55.0.5 is Git's version, not
// bash's).
func wingetStale(tool, pkg, have, upstream string) string {
	if versions.SameProduct(have, upstream) {
		return fmt.Sprintf("winget's %s has nothing newer than %s (upstream %s %s)",
			pkg, have, tool, upstream)
	}
	return fmt.Sprintf("winget's %s is %s, which is not a newer %s (upstream %s %s)",
		pkg, have, tool, tool, upstream)
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
