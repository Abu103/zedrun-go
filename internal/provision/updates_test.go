package provision

import (
	"strings"
	"testing"
)

// detectedWindows says winget is present, which is what makes the winget route
// exist at all.
func detectedWindows() map[string]string {
	return map[string]string{"winget": "/usr/bin/winget"}
}

// The Go case, from a real machine: go.dev has 1.27.3, winget's GoLang.Go
// manifest sits at 1.27.0 with nothing newer. The report must not promise 1.27.3.
func TestResolveUpdateSaysWhenWingetHasNothingNewer(t *testing.T) {
	ix := ix(wingetRow{name: "Go Programming Language", id: "GoLang.Go", version: "1.27.0", available: ""})
	got := ResolveUpdate("go", "windows", "1.27.0", "1.27.3", "winget", detectedWindows(), nil, ix)
	if got.Method.Runnable() {
		t.Fatalf("offered %q when winget has nothing newer", got.Method.Command())
	}
	if !strings.Contains(got.Reason, "nothing newer than 1.27.0") || !strings.Contains(got.Reason, "1.27.3") {
		t.Fatalf("reason does not name both versions: %q", got.Reason)
	}
}

// winget has the package and something newer: the command is offered, and the
// version promised is winget's, not upstream's.
func TestResolveUpdatePromisesWhatWingetWillInstall(t *testing.T) {
	ix := ix(wingetRow{name: "PowerShell 7-x64", id: "Microsoft.PowerShell", version: "7.6.6.0", available: "7.6.7.0"})
	got := ResolveUpdate("pwsh", "windows", "7.6.6", "7.6.7", "winget", detectedWindows(), nil, ix)
	if !got.Method.Runnable() {
		t.Fatalf("no command offered: %q", got.Reason)
	}
	if !strings.Contains(got.Method.Command(), "winget upgrade --id Microsoft.PowerShell") {
		t.Fatalf("command = %q", got.Method.Command())
	}
	if got.Achievable != "7.6.7.0" {
		t.Fatalf("Achievable = %q, want winget's 7.6.7.0", got.Achievable)
	}
}

// The lag case: winget's manifest is behind upstream. The achievable version is
// the honest one, and it is not upstream's.
func TestResolveUpdateReportsAWingetThatLagsUpstream(t *testing.T) {
	ix := ix(wingetRow{name: "Python 3.13.7 (64-bit)", id: "Python.Python.3.13", version: "3.13.7", available: "3.13.15"})
	got := ResolveUpdate("python", "windows", "3.13.7", "3.16.0", "winget", detectedWindows(), nil, ix)
	if !got.Method.Runnable() {
		t.Fatalf("no command offered: %q", got.Reason)
	}
	if got.Achievable != "3.13.15" {
		t.Fatalf("Achievable = %q, want 3.13.15 not upstream's 3.16.0", got.Achievable)
	}
}

// The table names Temurin.21, but the machine has Oracle.JDK.23. The table's id
// is not installed, so it is not used; the installed package is found by version.
func TestResolveUpdateFindsTheInstalledPackageWhenTheTableIsWrong(t *testing.T) {
	ix := ix(wingetRow{name: "Java(TM) SE Development Kit 23.0.2", id: "Oracle.JDK.23", version: "23.0.2.0", available: "23.0.4.0"})
	got := ResolveUpdate("java", "windows", "23.0.2", "25", "winget", detectedWindows(), nil, ix)
	if !got.Method.Runnable() {
		t.Fatalf("no command offered: %q", got.Reason)
	}
	if !strings.Contains(got.Method.Command(), "Oracle.JDK.23") {
		t.Fatalf("command names the table's package, not the installed one: %q", got.Method.Command())
	}
	if got.Achievable != "23.0.4.0" {
		t.Fatalf("Achievable = %q", got.Achievable)
	}
}

// The table's id is not installed and nothing matches by version: the route is
// dropped and the next manager gets a chance. This is the tsc case, where the
// fabricated NodeJS.LTS id used to be offered.
func TestResolveUpdateDropsAWingetPackageThatIsNotInstalled(t *testing.T) {
	ix := ix(wingetRow{name: "Go", id: "GoLang.Go", version: "1.27.0", available: ""})
	got := ResolveUpdate("node", "windows", "24.6.0", "24.22.0", "winget", detectedWindows(), nil, ix)
	if got.Method.Runnable() {
		t.Fatalf("offered %q for a package winget does not have", got.Method.Command())
	}
	// The origin claims winget, but winget's list does not have the package, so
	// the origin is stale and no manager can be confirmed as the owner.
	if got.Reason != "winget does not have node installed; upgrading through it would install a second copy beside the one here" {
		t.Fatalf("reason = %q", got.Reason)
	}
}

// A non-winget manager is offered as-is; the achievable version is unknown
// because nothing here can answer for it without running the command.
func TestResolveUpdateNonWingetManager(t *testing.T) {
	got := ResolveUpdate("python", "linux", "3.13.7", "3.16.0", "apt", map[string]string{"apt": "/usr/bin/apt-get"}, nil, nil)
	if !got.Method.Runnable() {
		t.Fatalf("no command offered: %q", got.Reason)
	}
	if !strings.Contains(got.Method.Command(), "apt-get install --only-upgrade -y python3") {
		t.Fatalf("command = %q", got.Method.Command())
	}
	if got.Achievable != "" {
		t.Fatalf("Achievable = %q, want unknown", got.Achievable)
	}
}

// With no winget index at all — winget absent, or not answering — no winget
// route is invented.
func TestResolveUpdateWithoutAWingetIndex(t *testing.T) {
	got := ResolveUpdate("go", "windows", "1.27.0", "1.27.3", "winget", detectedWindows(), nil, nil)
	if got.Method.Runnable() {
		t.Fatalf("offered %q with no winget to verify it", got.Method.Command())
	}
}
