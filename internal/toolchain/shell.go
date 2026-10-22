package toolchain

import (
	"path"
	"strings"

	"github.com/zedrun/zedrun-go/internal/platform"
)

// Shell names the shell an instruction is written for. A PATH line appended to
// ~/.bashrc does nothing for a zsh terminal, so every instruction carries the
// shell it is for instead of assuming one.
type Shell string

// Known shells. ShellUnknown means the shell could not be told apart, in which
// case the renderer prints a block for each plausible shell.
const (
	ShellUnknown    Shell = ""
	ShellBash       Shell = "bash"
	ShellZsh        Shell = "zsh"
	ShellFish       Shell = "fish"
	ShellSh         Shell = "sh"
	ShellPowerShell Shell = "powershell"
	ShellPwsh       Shell = "pwsh"
	ShellCmd        Shell = "cmd"
)

// ParseShell normalises a user-supplied shell name. It returns false for names
// zedrun-go has no instructions for.
func ParseShell(s string) (Shell, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ShellUnknown, true
	case "bash":
		return ShellBash, true
	case "zsh":
		return ShellZsh, true
	case "fish":
		return ShellFish, true
	case "sh", "dash", "ash":
		return ShellSh, true
	case "powershell", "ps", "ps1", "windowspowershell":
		return ShellPowerShell, true
	case "pwsh", "powershell-core":
		return ShellPwsh, true
	case "cmd", "cmd.exe":
		return ShellCmd, true
	}
	return ShellUnknown, false
}

// DetectShell guesses the interactive shell from the environment. An explicit
// ZEDRUN_SHELL wins. On POSIX SHELL names the login shell, which is the best
// available signal. On Windows nothing reliably separates cmd from PowerShell
// (PSModulePath is inherited by both), so the answer is ShellUnknown unless a
// hint is present, and callers then show both.
func DetectShell(env map[string]string, osName string) Shell {
	if s, ok := ParseShell(platform.Env(env, osName, "ZEDRUN_SHELL")); ok && s != ShellUnknown {
		return s
	}
	if osName == "windows" {
		// PROMPT is set by cmd.exe and not by PowerShell, so seeing it means a
		// cmd.exe ancestor, which is a hint, not proof.
		if platform.Env(env, osName, "PROMPT") != "" && platform.Env(env, osName, "PSExecutionPolicyPreference") == "" {
			return ShellCmd
		}
		return ShellUnknown
	}
	sh := platform.Env(env, osName, "SHELL")
	if sh == "" {
		return ShellUnknown
	}
	if s, ok := ParseShell(path.Base(strings.ReplaceAll(sh, `\`, "/"))); ok {
		return s
	}
	return ShellUnknown
}

// PathLine is the exact text that makes a directory permanent on PATH for one
// shell, and the file it belongs in. zedrun-go prints it; it never writes it.
type PathLine struct {
	Shell   Shell  `json:"shell"`
	File    string `json:"file,omitempty"`   // profile the line goes in; empty when the line is a one-off command
	Line    string `json:"line"`             // the exact text to append or run
	Session string `json:"session"`          // the same change for the current window only
	Reload  string `json:"reload,omitempty"` // how to pick it up without a new window
	Note    string `json:"note,omitempty"`
}

// PathAppend renders the line for dir in shell. dir is quoted for that shell,
// so a path with spaces, quotes or `$` is safe to paste.
func PathAppend(dir string, shell Shell, osName string) PathLine {
	switch shell {
	case ShellZsh:
		return PathLine{
			Shell: shell, File: "~/.zshrc",
			Line:    `export PATH=` + posixQuote(dir) + `:"$PATH"`,
			Session: `export PATH=` + posixQuote(dir) + `:"$PATH"`,
			Reload:  "source ~/.zshrc",
			Note:    "zsh reads ~/.zshrc; a line added to ~/.bashrc does not affect it",
		}
	case ShellFish:
		return PathLine{
			Shell: shell, File: "~/.config/fish/config.fish",
			Line:    "fish_add_path " + posixQuote(dir),
			Session: "fish_add_path " + posixQuote(dir),
			Reload:  "source ~/.config/fish/config.fish",
		}
	case ShellSh:
		return PathLine{
			Shell: shell, File: "~/.profile",
			Line:    `export PATH=` + posixQuote(dir) + `:"$PATH"`,
			Session: `export PATH=` + posixQuote(dir) + `:"$PATH"`,
			Reload:  ". ~/.profile",
		}
	case ShellPowerShell, ShellPwsh:
		persist := `[Environment]::SetEnvironmentVariable('Path', ` + psQuote(dir) + ` + ';' + [Environment]::GetEnvironmentVariable('Path','User'), 'User')`
		return PathLine{
			Shell: shell, File: "",
			Line:    persist,
			Session: `$env:PATH = ` + psQuote(dir) + ` + ';' + $env:PATH`,
			Reload:  "open a new PowerShell window",
			Note:    "this edits the user PATH in the registry, so it applies to every new window, not just PowerShell",
		}
	case ShellCmd:
		// setx PATH is a trap: it copies the merged machine+user PATH into the
		// user PATH and truncates at 1024 characters. The registry edit through
		// PowerShell has neither problem, and powershell.exe exists everywhere
		// cmd.exe does.
		persist := `powershell -NoProfile -Command "[Environment]::SetEnvironmentVariable('Path', ` +
			psQuote(dir) + ` + ';' + [Environment]::GetEnvironmentVariable('Path','User'), 'User')"`
		return PathLine{
			Shell: shell, File: "",
			Line:    persist,
			Session: `set "PATH=` + dir + `;%PATH%"`,
			Reload:  "open a new Command Prompt window",
			Note:    "do not use setx PATH: it truncates at 1024 characters and bakes the machine PATH into the user PATH",
		}
	}
	// bash, and the default for any POSIX shell we could not name.
	file := "~/.bashrc"
	reload := "source ~/.bashrc"
	note := "bash reads ~/.bashrc for interactive shells; a login shell reads ~/.bash_profile or ~/.profile instead"
	if osName == "macos" {
		file, reload = "~/.bash_profile", "source ~/.bash_profile"
		note = "Terminal on macOS starts bash as a login shell, which reads ~/.bash_profile, not ~/.bashrc"
	}
	return PathLine{
		Shell: ShellBash, File: file,
		Line:    `export PATH=` + posixQuote(dir) + `:"$PATH"`,
		Session: `export PATH=` + posixQuote(dir) + `:"$PATH"`,
		Reload:  reload,
		Note:    note,
	}
}

// ShellsFor lists the shells worth rendering for an OS when the real one is
// not known.
func ShellsFor(osName string) []Shell {
	if osName == "windows" {
		return []Shell{ShellPowerShell, ShellCmd}
	}
	return []Shell{ShellBash, ShellZsh}
}

// posixQuote single-quotes s for sh, bash and zsh. A single quote inside the
// string ends the quoting, emits an escaped quote and resumes, which is the
// only escape POSIX single quotes have. Paths that need no quoting stay bare.
func posixQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// psQuote single-quotes s for PowerShell, where the only escape is doubling an
// embedded single quote.
func psQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
