package provision

// packages maps a tool id to the package name each manager uses for it. A
// manager absent from a tool's map has no package for that tool; that is not an
// error, it means the route does not exist.
//
// CONFIDENCE. The "apt" column was checked against a real Ubuntu 24.04
// repository with `apt-cache policy`, so those names are known to resolve. The
// "apk" column was checked against the Alpine v3.21 package index at
// pkgs.alpinelinux.org: zig, dotnet8-sdk, openjdk21, php83, ghc, lua5.4,
// elixir and R are confirmed present, and kotlin and scala are confirmed absent
// (so they have no apk entry — a package name that cannot resolve is worse than
// no route). The dnf/pacman/zypper columns and the
// winget/choco/scoop/brew/nix columns are the distributions' and projects'
// documented names; none of those have been executed here, because only apt,
// snap, npm, pip, cargo and rustup exist on the machine this was written on.
// Treat them as claims to verify, and prefer `--all` when a route is suspect.
var packages = map[string]map[string]string{
	"gcc": {
		"apt": "gcc", "dnf": "gcc", "pacman": "gcc", "zypper": "gcc", "apk": "gcc",
		"winget": "MSYS2.MSYS2", "scoop": "gcc", "choco": "mingw", "brew": "gcc", "nix": "gcc",
	},
	"g++": {
		"apt": "g++", "dnf": "gcc-c++", "pacman": "gcc", "zypper": "gcc-c++", "apk": "g++",
		"winget": "MSYS2.MSYS2", "scoop": "gcc", "choco": "mingw", "brew": "gcc", "nix": "gcc",
	},
	"clang": {
		"apt": "clang", "dnf": "clang", "pacman": "clang", "zypper": "clang", "apk": "clang",
		"winget": "LLVM.LLVM", "scoop": "llvm", "choco": "llvm", "brew": "llvm", "nix": "llvm",
	},
	"clang++": {
		"apt": "clang", "dnf": "clang", "pacman": "clang", "zypper": "clang", "apk": "clang",
		"winget": "LLVM.LLVM", "scoop": "llvm", "choco": "llvm", "brew": "llvm", "nix": "llvm",
	},
	"go": {
		"apt": "golang-go", "dnf": "golang", "pacman": "go", "zypper": "go", "apk": "go",
		"snap": "go", "winget": "GoLang.Go", "scoop": "go", "choco": "golang",
		"brew": "go", "nix": "go",
	},
	"rustc": {
		"apt": "rustc", "dnf": "rust", "pacman": "rust", "zypper": "rust", "apk": "rust",
		"rustup": "stable", "winget": "Rustlang.Rustup", "scoop": "rustup", "choco": "rust",
		"brew": "rust", "nix": "rustc",
	},
	"cargo": {
		"apt": "cargo", "dnf": "cargo", "pacman": "rust", "zypper": "cargo", "apk": "cargo",
		"rustup": "stable", "winget": "Rustlang.Rustup", "scoop": "rustup", "choco": "rust",
		"brew": "rust", "nix": "cargo",
	},
	"node": {
		"apt": "nodejs", "dnf": "nodejs", "pacman": "nodejs", "zypper": "nodejs", "apk": "nodejs",
		"snap": "node", "winget": "OpenJS.NodeJS.LTS", "scoop": "nodejs", "choco": "nodejs-lts",
		"brew": "node", "nix": "nodejs",
	},
	"bun": {
		"winget": "Oven-sh.Bun", "scoop": "bun", "choco": "bun", "brew": "oven-sh/bun/bun",
	},
	"deno": {
		"winget": "DenoLand.Deno", "scoop": "deno", "choco": "deno", "brew": "deno", "nix": "deno",
	},
	"python": {
		"apt": "python3", "dnf": "python3", "pacman": "python", "zypper": "python3", "apk": "python3",
		"winget": "Python.Python.3.13", "scoop": "python", "choco": "python",
		"brew": "python", "nix": "python3",
	},
	"java": {
		"apt": "default-jdk", "dnf": "java-21-openjdk-devel", "pacman": "jdk-openjdk",
		"zypper": "java-21-openjdk-devel", "apk": "openjdk21",
		"winget": "EclipseAdoptium.Temurin.21.JDK", "scoop": "temurin21-jdk", "choco": "temurin21",
		"brew": "temurin", "nix": "temurin-bin-21",
	},
	"javac": {
		"apt": "default-jdk", "dnf": "java-21-openjdk-devel", "pacman": "jdk-openjdk",
		"zypper": "java-21-openjdk-devel", "apk": "openjdk21",
		"winget": "EclipseAdoptium.Temurin.21.JDK", "scoop": "temurin21-jdk", "choco": "temurin21",
		"brew": "temurin", "nix": "temurin-bin-21",
	},
	"dotnet": {
		"apt": "dotnet-sdk-8.0", "dnf": "dotnet-sdk-8.0", "pacman": "dotnet-sdk",
		"zypper": "dotnet-sdk-8.0", "apk": "dotnet8-sdk",
		"winget": "Microsoft.DotNet.SDK.8", "scoop": "dotnet-sdk", "choco": "dotnet-sdk",
		"brew": "dotnet", "nix": "dotnet-sdk",
	},
	"dart": {
		"snap": "dart", "winget": "Google.Dart.SDK", "scoop": "dart", "choco": "dart-sdk",
		"brew": "dart", "nix": "dart",
	},
	"elixir": {
		"apt": "elixir", "dnf": "elixir", "pacman": "elixir", "zypper": "elixir", "apk": "elixir",
		"winget": "Elixir.Elixir", "scoop": "elixir", "choco": "elixir", "brew": "elixir", "nix": "elixir",
	},
	"julia": {
		"winget": "Julialang.Julia", "scoop": "julia", "choco": "julia", "brew": "julia", "nix": "julia",
	},
	"kotlin": {
		// No apk entry: kotlin is not in the Alpine v3.21 index, so offering
		// one would produce an install that cannot resolve.
		"apt": "kotlin", "dnf": "kotlin", "pacman": "kotlin", "snap": "kotlin",
		"winget": "Kotlin.Kotlin", "scoop": "kotlin", "choco": "kotlin", "brew": "kotlin",
	},
	"lua": {
		"apt": "lua5.4", "dnf": "lua", "pacman": "lua", "zypper": "lua5.4", "apk": "lua5.4",
		"winget": "DEVCOM.Lua", "scoop": "lua", "choco": "lua", "brew": "lua", "nix": "lua5_4",
	},
	"perl": {
		"apt": "perl", "dnf": "perl", "pacman": "perl", "zypper": "perl", "apk": "perl",
		"winget": "StrawberryPerl.StrawberryPerl", "scoop": "perl", "choco": "perl",
		"brew": "perl", "nix": "perl",
	},
	"php": {
		"apt": "php-cli", "dnf": "php-cli", "pacman": "php", "zypper": "php-cli", "apk": "php83",
		"winget": "PHP.PHP", "scoop": "php", "choco": "php", "brew": "php", "nix": "php",
	},
	"r": {
		"apt": "r-base", "dnf": "R", "pacman": "r", "zypper": "R-base", "apk": "R",
		"winget": "RProject.R", "scoop": "r", "choco": "r.project", "brew": "r", "nix": "R",
	},
	"ruby": {
		"apt": "ruby-full", "dnf": "ruby", "pacman": "ruby", "zypper": "ruby", "apk": "ruby",
		"winget": "RubyInstallerTeam.Ruby", "scoop": "ruby", "choco": "ruby",
		"brew": "ruby", "nix": "ruby",
	},
	"scala": {
		// No apk entry: scala is not in the Alpine v3.21 index.
		"apt": "scala", "dnf": "scala", "pacman": "scala", "snap": "scala",
		"winget": "Scala.Scala", "scoop": "scala", "choco": "scala", "brew": "scala", "nix": "scala",
	},
	"bash": {
		"apt": "bash", "dnf": "bash", "pacman": "bash", "zypper": "bash", "apk": "bash",
		"winget": "Git.Git", "scoop": "git", "choco": "git", "brew": "bash", "nix": "bash",
	},
	"sh": {
		"apt": "dash", "dnf": "bash", "pacman": "bash", "zypper": "bash", "apk": "busybox",
		"winget": "Git.Git", "scoop": "git", "choco": "git", "brew": "bash", "nix": "bash",
	},
	"sqlite3": {
		"apt": "sqlite3", "dnf": "sqlite", "pacman": "sqlite", "zypper": "sqlite3", "apk": "sqlite",
		"winget": "SQLite.SQLite", "scoop": "sqlite", "choco": "sqlite", "brew": "sqlite", "nix": "sqlite",
	},
	"zig": {
		// Alpine has zig in community (verified against the v3.21 index).
		"apk": "zig", "snap": "zig", "winget": "zig.zig", "scoop": "zig", "choco": "zig",
		"brew": "zig", "nix": "zig",
	},
	"swift": {
		"winget": "Swift.Toolchain", "scoop": "swift", "choco": "swift", "brew": "swift",
	},
	"pwsh": {
		"snap": "powershell", "winget": "Microsoft.PowerShell", "scoop": "pwsh",
		"choco": "powershell-core", "brew": "powershell", "nix": "powershell",
	},
	"ghc": {
		"apt": "ghc", "dnf": "ghc", "pacman": "ghc", "apk": "ghc",
		"winget": "haskell.haskell", "scoop": "ghc", "choco": "ghc", "brew": "ghc", "nix": "ghc",
	},
	"runghc": {
		"apt": "ghc", "dnf": "ghc", "pacman": "ghc", "apk": "ghc",
		"winget": "haskell.haskell", "scoop": "ghc", "choco": "ghc", "brew": "ghc", "nix": "ghc",
	},
	"tsc": {
		"npm": "typescript", "winget": "OpenJS.NodeJS.LTS", "scoop": "nodejs", "choco": "nodejs-lts",
		"brew": "node",
	},
	"msvc": {
		"winget": "Microsoft.VisualStudio.2022.BuildTools",
		"choco":  "visualstudio2022buildtools", "scoop": "vsbuildtools",
	},
}

// directURL is where to get a tool that no package manager on this machine can
// supply, and the one-line explanation shown beside it.
//
// These exist because the gap is real: when this table was written, zig, julia,
// dart, pwsh, swift and temurin were all absent from an Ubuntu 24.04 repository,
// so on Linux the only honest answer for them is a download.
var directURL = map[string]struct{ URL, Note string }{
	"bun":   {"https://bun.sh/install", "curl -fsSL https://bun.sh/install | bash"},
	"deno":  {"https://deno.land/install.sh", "curl -fsSL https://deno.land/install.sh | sh"},
	"zig":   {"https://ziglang.org/download/", "download the tarball for your platform"},
	"julia": {"https://julialang.org/downloads/", "download the official installer"},
	"dart":  {"https://dart.dev/get-dart", "add the Dart apt repository, or use the SDK zip"},
	"swift": {"https://www.swift.org/install/", "download the Swift toolchain for your distribution"},
	"pwsh":  {"https://github.com/PowerShell/PowerShell/releases", "download the .deb, .rpm or .tar.gz"},
	"dotnet": {"https://dotnet.microsoft.com/download",
		"the Microsoft feed is needed when the distro package is not available"},
	"msvc": {"https://visualstudio.microsoft.com/downloads/",
		"install the Visual Studio Build Tools, then run vcvarsall"},
	"ghc":    {"https://www.haskell.org/ghcup/", "ghcup is the supported installer"},
	"runghc": {"https://www.haskell.org/ghcup/", "ghcup is the supported installer"},
	"java":   {"https://adoptium.net/temurin/releases/", "download a Temurin JDK build"},
	"javac":  {"https://adoptium.net/temurin/releases/", "download a Temurin JDK build"},
	"kotlin": {"https://github.com/JetBrains/kotlin/releases", "or use sdkman: sdk install kotlin"},
	"scala":  {"https://www.scala-lang.org/download/", "or use sdkman: sdk install scala"},
	"tsc":    {"https://www.typescriptlang.org/download", "npm install -g typescript"},
}

// windowsOnly are tools that do not exist on Linux at all, so having no route
// there is correct rather than a gap in the table.
var windowsOnly = map[string]bool{"msvc": true}
