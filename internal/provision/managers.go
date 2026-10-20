// Package provision answers "how do I get this toolchain on this machine?" and,
// when asked, does it.
//
// Nothing here executes anything until Run is called. managers.go is a data
// table plus detection, methods.go maps tools to packages, and provision.go
// filters, orders, renders and runs. Run takes argv straight from the table
// rather than building a shell string, so there is no shell-injection surface.
package provision

import (
	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// Manager is a package manager zedrun-go can drive.
type Manager struct {
	Name     string   // "apt", "winget", ...
	Probe    string   // executable to look for; empty means Name
	Version  []string // argv that makes the manager print its version and exit 0
	Install  []string // argv template; "{package}" is replaced
	Update   []string // argv template; "{package}" is replaced
	Elevated bool     // needs administrator or root
	OSes     []string // empty means every OS
}

// managerByOS groups the manager table by the OSes it runs on, preserving the
// table's order within each group. Available walks this instead of ranging over
// Known directly: ranging over a map would hand back a different order on every
// call, which made the starred "--run" choice move between runs and made the
// output impossible to compare in tests or in a terminal scrollback.
var managerByOS = map[string][]Manager{}

// Known is every manager zedrun-go can drive, in preference order within an OS.
//
// The order matters: a tool with a package in two present managers is offered
// the earlier one first, so the distro's own manager beats a language-specific
// one, and winget beats the community Windows managers.
var Known = []Manager{
	// --- Windows ---
	{Name: "winget", OSes: []string{"windows"},
		Version: []string{"--version"},
		Install: []string{"winget", "install", "--id", "{package}",
			"--accept-source-agreements", "--accept-package-agreements"},
		Update: []string{"winget", "upgrade", "--id", "{package}",
			"--accept-source-agreements"}},
	{Name: "scoop", OSes: []string{"windows"},
		Version: []string{"--version"},
		Install: []string{"scoop", "install", "{package}"},
		Update:  []string{"scoop", "update", "{package}"}},
	{Name: "choco", OSes: []string{"windows"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"choco", "install", "-y", "{package}"},
		Update:  []string{"choco", "upgrade", "-y", "{package}"}},

	// --- Linux distributions ---
	{Name: "apt", Probe: "apt-get", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"apt-get", "install", "-y", "{package}"},
		Update:  []string{"apt-get", "install", "--only-upgrade", "-y", "{package}"}},
	{Name: "dnf", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"dnf", "install", "-y", "{package}"},
		Update:  []string{"dnf", "upgrade", "-y", "{package}"}},
	{Name: "pacman", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"pacman", "-S", "--noconfirm", "{package}"},
		Update:  []string{"pacman", "-S", "--noconfirm", "{package}"}},
	{Name: "zypper", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"zypper", "--non-interactive", "install", "{package}"},
		Update:  []string{"zypper", "--non-interactive", "update", "{package}"}},
	{Name: "apk", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"apk", "add", "{package}"},
		Update:  []string{"apk", "upgrade", "{package}"}},
	{Name: "snap", OSes: []string{"linux"}, Elevated: true,
		Version: []string{"--version"},
		Install: []string{"snap", "install", "{package}"},
		Update:  []string{"snap", "refresh", "{package}"}},

	// --- language-specific, cross-platform ---
	{Name: "rustup",
		Version: []string{"--version"},
		Install: []string{"rustup", "toolchain", "install", "{package}"},
		Update:  []string{"rustup", "update", "{package}"}},
	{Name: "npm",
		Version: []string{"--version"},
		Install: []string{"npm", "install", "-g", "{package}"},
		Update:  []string{"npm", "install", "-g", "{package}@latest"}},
	{Name: "pip", Probe: "pip3",
		Version: []string{"--version"},
		Install: []string{"pip3", "install", "--user", "{package}"},
		Update:  []string{"pip3", "install", "--user", "--upgrade", "{package}"}},
	{Name: "cargo",
		Version: []string{"--version"},
		Install: []string{"cargo", "install", "{package}"},
		Update:  []string{"cargo", "install", "{package}"}},
	{Name: "go",
		Version: []string{"version"},
		Install: []string{"go", "install", "{package}"},
		Update:  []string{"go", "install", "{package}"}},

	// --- cross-platform, last because they are the least predictable ---
	{Name: "brew", OSes: []string{"linux", "macos"},
		Version: []string{"--version"},
		Install: []string{"brew", "install", "{package}"},
		Update:  []string{"brew", "upgrade", "{package}"}},
	{Name: "nix", Probe: "nix-env",
		Version: []string{"--version"},
		Install: []string{"nix-env", "-iA", "{package}"},
		Update:  []string{"nix-env", "-u", "{package}"}},
}

// everyOS is the set an empty OSes list means. macOS is still listed because
// the manager table is shared: a route offered on macOS is simply never shown
// on a machine that is not running it.
var everyOS = []string{"windows", "linux", "macos"}

func init() {
	for _, m := range Known {
		if len(m.OSes) == 0 {
			for _, osName := range everyOS {
				managerByOS[osName] = append(managerByOS[osName], m)
			}
			continue
		}
		for _, osName := range m.OSes {
			managerByOS[osName] = append(managerByOS[osName], m)
		}
	}
}

// probe is the executable a manager is detected by.
func (m Manager) probe() string {
	if m.Probe != "" {
		return m.Probe
	}
	return m.Name
}

// SupportsOS reports whether a manager runs on an OS. An empty OSes means every
// OS, which is how the cross-platform managers are declared.
func (m Manager) SupportsOS(osName string) bool {
	if len(m.OSes) == 0 {
		return true
	}
	for _, o := range m.OSes {
		if o == osName {
			return true
		}
	}
	return false
}

// Detected maps manager name to the path it was found at, for the managers that
// exist on this machine and run on this OS.
//
// Detection reuses the toolchain prober, so "is winget here" is the same
// question as "is gcc here" and needs no new machinery.
func Detected(cfg config.Config, cache *detect.ProbeCache, env map[string]string, osName string) map[string]string {
	out := map[string]string{}
	for _, m := range Known {
		if !m.SupportsOS(osName) {
			continue
		}
		t := catalog.Tool{ID: m.Name, Names: []string{m.probe()}, VersionArgs: m.Version}
		for _, in := range detect.FindInstalls(t, cfg, cache, false, env, osName) {
			if in.OK {
				out[m.Name] = in.Path
				break
			}
		}
	}
	return out
}

// managerNamed returns the manager with this name.
func managerNamed(name string) (Manager, bool) {
	for _, m := range Known {
		if m.Name == name {
			return m, true
		}
	}
	return Manager{}, false
}
