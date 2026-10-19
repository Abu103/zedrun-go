// Package catalog is the data layer: which tools exist, how to ask each for its
// version, and how to run each language. Runners are ordered command templates;
// the planner resolves them against detected installs.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tool describes an executable zedrun-go can look for.
type Tool struct {
	ID          string
	Names       []string // executable names to try, in order
	VersionArgs []string // argv appended to the executable to print its version
	Dirs        []string // extra directories to search, relative to a home dir
	OS          []string // if set, the tool only exists on these OS names
	Special     string   // "msvc" routes discovery through vswhere/vcvars
}

// Runner is one way to run a language: the tools it needs, the command steps,
// and the conditions under which it applies.
type Runner struct {
	Tool    []string   // tool ids, tried in order; the first usable one wins
	Steps   [][]string // argv templates; a failing step stops the pipeline
	When    []string   // conditions, see planner.CheckWhen
	Cwd     string     // "" = the file's directory; "{root}" = project root
	Release []string   // extra flags added by --release
	Kind    string     // "compile" writes an artifact; "project" needs a manifest
	Std     string     // default value for the {std} splice
	Libs    string     // default value for the {libs} splice
}

// Language maps file extensions and shebangs to ordered runners.
type Language struct {
	Key      string
	Exts     []string
	Runners  []Runner
	Shebangs []string
}

// KnownPlaceholders are the scalar and splice placeholders a step may use.
// {tool:ID} is handled separately.
var KnownPlaceholders = map[string]bool{
	"tool": true, "file": true, "dir": true, "stem": true, "name": true,
	"exe": true, "jar": true, "obj": true, "root": true, "posix_file": true,
	"flags": true, "profile": true, "std": true, "libs": true, "args": true,
}

// Tools is every executable zedrun-go knows how to find and version.
var Tools = map[string]Tool{
	"gcc":     {ID: "gcc", Names: []string{"gcc"}, VersionArgs: []string{"--version"}},
	"g++":     {ID: "g++", Names: []string{"g++"}, VersionArgs: []string{"--version"}},
	"clang":   {ID: "clang", Names: []string{"clang"}, VersionArgs: []string{"--version"}},
	"clang++": {ID: "clang++", Names: []string{"clang++"}, VersionArgs: []string{"--version"}},
	"msvc":    {ID: "msvc", Names: []string{"cl"}, Special: "msvc"},
	"go":      {ID: "go", Names: []string{"go"}, VersionArgs: []string{"version"}},
	"rustc":   {ID: "rustc", Names: []string{"rustc"}, VersionArgs: []string{"--version"}},
	"cargo":   {ID: "cargo", Names: []string{"cargo"}, VersionArgs: []string{"--version"}},
	"node":    {ID: "node", Names: []string{"node"}, VersionArgs: []string{"--version"}},
	"bun":     {ID: "bun", Names: []string{"bun"}, VersionArgs: []string{"--version"}},
	"deno":    {ID: "deno", Names: []string{"deno"}, VersionArgs: []string{"--version"}},
	"python":  {ID: "python", Names: []string{"python3", "python"}, VersionArgs: []string{"--version"}},
	"java":    {ID: "java", Names: []string{"java"}, VersionArgs: []string{"-version"}},
	"javac":   {ID: "javac", Names: []string{"javac"}, VersionArgs: []string{"-version"}},
	"dotnet":  {ID: "dotnet", Names: []string{"dotnet"}, VersionArgs: []string{"--version"}},
	"dart":    {ID: "dart", Names: []string{"dart"}, VersionArgs: []string{"--version"}},
	"elixir":  {ID: "elixir", Names: []string{"elixir"}, VersionArgs: []string{"--version"}},
	"julia":   {ID: "julia", Names: []string{"julia"}, VersionArgs: []string{"--version"}},
	"kotlin":  {ID: "kotlin", Names: []string{"kotlinc"}, VersionArgs: []string{"-version"}},
	"lua":     {ID: "lua", Names: []string{"lua"}, VersionArgs: []string{"-v"}},
	"perl":    {ID: "perl", Names: []string{"perl"}, VersionArgs: []string{"-v"}},
	"php":     {ID: "php", Names: []string{"php"}, VersionArgs: []string{"--version"}},
	"pwsh":    {ID: "pwsh", Names: []string{"pwsh"}, VersionArgs: []string{"--version"}},
	"r":       {ID: "r", Names: []string{"Rscript"}, VersionArgs: []string{"--version"}},
	"ruby":    {ID: "ruby", Names: []string{"ruby"}, VersionArgs: []string{"--version"}},
	"scala":   {ID: "scala", Names: []string{"scala"}, VersionArgs: []string{"-version"}},
	"bash":    {ID: "bash", Names: []string{"bash"}, VersionArgs: []string{"--version"}},
	"sh":      {ID: "sh", Names: []string{"sh"}, VersionArgs: []string{"-c", "echo ok"}},
	"sqlite3": {ID: "sqlite3", Names: []string{"sqlite3"}, VersionArgs: []string{"--version"}},
	"swift":   {ID: "swift", Names: []string{"swift"}, VersionArgs: []string{"--version"}},
	"zig":     {ID: "zig", Names: []string{"zig"}, VersionArgs: []string{"version"}},
	"tsc":     {ID: "tsc", Names: []string{"tsc"}, VersionArgs: []string{"--version"}},
	"ghc":     {ID: "ghc", Names: []string{"ghc"}, VersionArgs: []string{"--version"}},
	"runghc":  {ID: "runghc", Names: []string{"runghc"}, VersionArgs: []string{"--version"}},
}

// Languages is every language zedrun-go can run, keyed by language name.
var Languages = map[string]Language{
	"c": {Key: "c", Exts: []string{".c"}, Runners: []Runner{
		{Tool: []string{"clang", "gcc"}, Kind: "compile", Std: "-std=c17", Libs: "-lm", Release: []string{"-O2"},
			Steps: [][]string{{"{tool}", "{std}", "{flags}", "-o", "{exe}", "{file}", "{libs}"}, {"{exe}", "{args}"}}},
		{Tool: []string{"msvc"}, Kind: "compile", Release: []string{"/O2"},
			Steps: [][]string{{"{tool}", "{flags}", "{file}", "/Fe:{exe}"}, {"{exe}", "{args}"}}},
	}},
	"cpp": {Key: "cpp", Exts: []string{".cpp", ".cc", ".cxx", ".c++"}, Runners: []Runner{
		{Tool: []string{"clang++", "g++"}, Kind: "compile", Std: "-std=c++17", Release: []string{"-O2"},
			Steps: [][]string{{"{tool}", "{std}", "{flags}", "-o", "{exe}", "{file}"}, {"{exe}", "{args}"}}},
		{Tool: []string{"msvc"}, Kind: "compile", Release: []string{"/O2"},
			Steps: [][]string{{"{tool}", "{flags}", "{file}", "/Fe:{exe}"}, {"{exe}", "{args}"}}},
	}},
	"csharp": {Key: "csharp", Exts: []string{".cs"}, Runners: []Runner{
		{Tool: []string{"dotnet"}, Kind: "project", When: []string{"project:*.csproj"}, Cwd: "{root}",
			Steps: [][]string{{"{tool}", "run", "--quiet", "--", "{args}"}}},
		{Tool: []string{"dotnet"}, Kind: "compile",
			Steps: [][]string{{"{tool}", "run", "--file", "{file}", "--", "{args}"}}},
	}},
	"dart": {Key: "dart", Exts: []string{".dart"}, Runners: []Runner{
		{Tool: []string{"dart"}, Steps: [][]string{{"{tool}", "run", "{file}", "{args}"}}},
	}},
	"elixir": {Key: "elixir", Exts: []string{".exs", ".ex"}, Runners: []Runner{
		{Tool: []string{"elixir"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"go": {Key: "go", Exts: []string{".go"}, Runners: []Runner{
		{Tool: []string{"go"}, Steps: [][]string{{"{tool}", "run", "{file}", "{args}"}}},
	}},
	"haskell": {Key: "haskell", Exts: []string{".hs"}, Runners: []Runner{
		{Tool: []string{"runghc"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"java": {Key: "java", Exts: []string{".java"}, Runners: []Runner{
		{Tool: []string{"javac", "java"}, Kind: "compile",
			Steps: [][]string{{"{tool:javac}", "-d", "{dir}", "{file}"}, {"{tool:java}", "-cp", "{dir}", "{stem}", "{args}"}}},
	}},
	"javascript": {Key: "javascript", Exts: []string{".js", ".mjs", ".cjs"}, Runners: []Runner{
		{Tool: []string{"node"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
		{Tool: []string{"bun"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
		{Tool: []string{"deno"}, Steps: [][]string{{"{tool}", "run", "--allow-all", "{file}", "{args}"}}},
	}},
	"julia": {Key: "julia", Exts: []string{".jl"}, Runners: []Runner{
		{Tool: []string{"julia"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"kotlin": {Key: "kotlin", Exts: []string{".kt", ".kts"}, Runners: []Runner{
		{Tool: []string{"kotlin"}, Steps: [][]string{{"{tool}", "-script", "{file}", "{args}"}}},
		{Tool: []string{"kotlin", "java"}, Kind: "compile",
			Steps: [][]string{{"{tool:kotlin}", "{file}", "-include-runtime", "-d", "{jar}"}, {"{tool:java}", "-jar", "{jar}", "{args}"}}},
	}},
	"lua": {Key: "lua", Exts: []string{".lua"}, Runners: []Runner{
		{Tool: []string{"lua"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"perl": {Key: "perl", Exts: []string{".pl", ".pm"}, Runners: []Runner{
		{Tool: []string{"perl"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"php": {Key: "php", Exts: []string{".php"}, Runners: []Runner{
		{Tool: []string{"php"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"powershell": {Key: "powershell", Exts: []string{".ps1"}, Runners: []Runner{
		{Tool: []string{"pwsh"}, Steps: [][]string{{"{tool}", "-NoProfile", "-File", "{file}", "{args}"}}},
	}},
	"python": {Key: "python", Exts: []string{".py"}, Shebangs: []string{"python"},
		Runners: []Runner{{Tool: []string{"python"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}}}},
	"r": {Key: "r", Exts: []string{".r"}, Shebangs: []string{"Rscript"}, Runners: []Runner{
		{Tool: []string{"r"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"ruby": {Key: "ruby", Exts: []string{".rb"}, Shebangs: []string{"ruby"}, Runners: []Runner{
		{Tool: []string{"ruby"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"rust": {Key: "rust", Exts: []string{".rs"}, Runners: []Runner{
		{Tool: []string{"cargo"}, Kind: "project", When: []string{"project:Cargo.toml"}, Cwd: "{root}", Release: []string{"--release"},
			Steps: [][]string{{"{tool}", "run", "--quiet", "--", "{args}"}}},
		{Tool: []string{"rustc"}, Kind: "compile", Release: []string{"-O"},
			Steps: [][]string{{"{tool}", "-o", "{exe}", "{file}"}, {"{exe}", "{args}"}}},
	}},
	"scala": {Key: "scala", Exts: []string{".scala", ".sc"}, Runners: []Runner{
		{Tool: []string{"scala"}, Steps: [][]string{{"{tool}", "run", "{file}", "--", "{args}"}}},
	}},
	"shell": {Key: "shell", Exts: []string{".sh", ".bash"}, Shebangs: []string{"bash", "/sh", "sh"}, Runners: []Runner{
		{Tool: []string{"bash"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
		{Tool: []string{"sh"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"sql": {Key: "sql", Exts: []string{".sql"}, Runners: []Runner{
		{Tool: []string{"sqlite3"}, Steps: [][]string{{"{tool}", ":memory:", ".read {file}"}}},
	}},
	"swift": {Key: "swift", Exts: []string{".swift"}, Runners: []Runner{
		{Tool: []string{"swift"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
	}},
	"typescript": {Key: "typescript", Exts: []string{".ts", ".mts", ".cts"}, Runners: []Runner{
		{Tool: []string{"tsc", "node"}, Kind: "compile",
			Steps: [][]string{{"{tool:tsc}", "{file}", "--target", "es2022"}, {"{tool:node}", "{dir}/{stem}.js", "{args}"}}},
		{Tool: []string{"bun"}, Steps: [][]string{{"{tool}", "{file}", "{args}"}}},
		{Tool: []string{"deno"}, Steps: [][]string{{"{tool}", "run", "--allow-all", "{file}", "{args}"}}},
	}},
	"zig": {Key: "zig", Exts: []string{".zig"}, Runners: []Runner{
		{Tool: []string{"zig"}, Steps: [][]string{{"{tool}", "run", "{file}", "{args}"}}},
	}},
}

// installHints suggest how to get a tool, per OS. "any" is the fallback.
var installHints = map[string]map[string]string{
	"gcc":    {"windows": "install MSYS2 (ucrt64) or MinGW-w64, then add its bin to PATH", "macos": "xcode-select --install", "linux": "apt install build-essential (Debian) or dnf install gcc (Fedora)"},
	"g++":    {"windows": "install MSYS2 (ucrt64) or MinGW-w64", "macos": "xcode-select --install", "linux": "apt install g++"},
	"clang":  {"windows": "winget install LLVM.LLVM", "macos": "xcode-select --install", "linux": "apt install clang"},
	"go":     {"windows": "winget install GoLang.Go", "macos": "brew install go", "linux": "apt install golang-go or download from go.dev/dl"},
	"rustc":  {"any": "rustup (https://rustup.rs) installs rustc and cargo"},
	"cargo":  {"any": "rustup (https://rustup.rs)"},
	"node":   {"windows": "winget install OpenJS.NodeJS.LTS", "macos": "brew install node", "linux": "nvm, or apt install nodejs"},
	"bun":    {"any": "curl -fsSL https://bun.sh/install | bash"},
	"deno":   {"any": "curl -fsSL https://deno.land/install.sh | sh"},
	"python": {"windows": "winget install Python.Python.3.12", "macos": "brew install python", "linux": "apt install python3"},
	"java":   {"any": "install a JDK (Adoptium Temurin, sdkman, or your package manager)"},
	"javac":  {"any": "install a JDK (Adoptium Temurin, sdkman, or your package manager)"},
	"dotnet": {"any": "download the .NET SDK from dotnet.microsoft.com"},
	"dart":   {"any": "install the Dart SDK or Flutter"},
	"elixir": {"any": "install Erlang/OTP then Elixir"},
	"julia":  {"any": "install Julia from julialang.org"},
	"kotlin": {"any": "install the Kotlin compiler (sdkman: sdk install kotlin)"},
	"lua":    {"any": "install Lua from lua.org or your package manager"},
	"perl":   {"any": "install Perl from perl.org or your package manager"},
	"php":    {"any": "install PHP from php.net or your package manager"},
	"pwsh":   {"any": "install PowerShell 7 from Microsoft"},
	"r":      {"any": "install R from r-project.org"},
	"ruby":   {"any": "install Ruby from ruby-lang.org or your package manager"},
	"scala":  {"any": "install Scala (sdkman: sdk install scala)"},
	"bash":   {"windows": "install Git for Windows or MSYS2", "any": "bash ships with macOS and Linux"},
	"sh":     {"windows": "install Git for Windows or MSYS2"},
	"sqlite3": {"any": "install the SQLite CLI (sqlite3) from sqlite.org"},
	"swift":  {"windows": "install Swift for Windows", "macos": "xcode-select --install", "linux": "install swiftlang"},
	"zig":    {"any": "download Zig from ziglang.org"},
	"tsc":    {"any": "npm install -g typescript"},
	"runghc": {"any": "install the Haskell toolchain (ghcup: https://www.haskell.org/ghcup/)"},
}

// GetTool returns a tool by id, or an ad-hoc tool for an unknown id so a custom
// language can still be probed by name.
func GetTool(id string) Tool {
	if t, ok := Tools[id]; ok {
		return t
	}
	return Tool{ID: id, Names: []string{id}, VersionArgs: []string{"--version"}}
}

// InstallHint returns how to install a tool on an OS, or a generic message.
func InstallHint(toolID, osName string) string {
	if h, ok := installHints[toolID]; ok {
		if s, ok := h[osName]; ok {
			return s
		}
		if s, ok := h["any"]; ok {
			return s
		}
	}
	return fmt.Sprintf("install %s and make sure it is on your PATH", toolID)
}

// LanguageForPath picks a language by file extension first, then by shebang.
func LanguageForPath(path string, langs map[string]Language) (string, bool) {
	if langs == nil {
		langs = Languages
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		for key, l := range langs {
			for _, e := range l.Exts {
				if e == ext {
					return key, true
				}
			}
		}
	}
	if line, ok := firstLine(path); ok {
		for key, l := range langs {
			for _, sb := range l.Shebangs {
				if strings.Contains(line, sb) {
					return key, true
				}
			}
		}
	}
	return "", false
}

func firstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	if n == 0 {
		return "", false
	}
	line := string(buf[:n])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line, true
}

// BuildCustom turns a config language spec into a Language: exts plus one or
// more command templates.
func BuildCustom(key string, spec map[string]any) Language {
	l := Language{Key: key}
	for _, e := range toStrings(spec["exts"]) {
		if e = ensureDot(strings.ToLower(strings.TrimSpace(e))); e != "." {
			l.Exts = append(l.Exts, e)
		}
	}
	toolID := "custom"
	if t := toStrings(spec["tool"]); len(t) > 0 {
		toolID = t[0]
	}
	var steps [][]string
	for _, cmd := range toStrings(spec["run"]) {
		if argv := splitCommand(cmd); len(argv) > 0 {
			steps = append(steps, argv)
		}
	}
	if len(steps) > 0 {
		l.Runners = append(l.Runners, Runner{Tool: []string{toolID}, Steps: steps})
	}
	return l
}

func toStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

func ensureDot(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, ".") {
		return s
	}
	return "." + s
}

// splitCommand splits a command template on whitespace, keeping quoted runs
// whole so a path with spaces survives.
func splitCommand(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
