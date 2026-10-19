// Package dist tests the distribution surface: the one-line installers, the
// release workflow and the docs that tell people how to use them.
package dist

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd)) // internal/dist -> repo root
}

func TestInstallShHelp(t *testing.T) {
	out, err := exec.Command("sh", filepath.Join(repoRoot(t), "install.sh"), "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "usage") {
		t.Fatalf("--help printed no usage:\n%s", out)
	}
}

func TestInstallShUnknownFlag(t *testing.T) {
	if err := exec.Command("sh", filepath.Join(repoRoot(t), "install.sh"), "--nope").Run(); err == nil {
		t.Fatal("an unknown flag exited 0")
	}
}

func TestInstallShPlacesBinaryAndUninstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix installer")
	}
	script := filepath.Join(repoRoot(t), "install.sh")

	dir := t.TempDir()
	osName, arch := "linux", "amd64"
	if runtime.GOOS == "darwin" {
		osName = "darwin"
	}
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	asset := filepath.Join(dir, "zedrun-go-"+osName+"-"+arch)
	body := "#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then echo 'zedrun-go 9.9.9'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(asset, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	prefix := filepath.Join(dir, "bin")
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(),
		"ZEDRUN_BASE_URL=file://"+dir,
		"ZEDRUN_HOME="+prefix,
		"ZEDRUN_OS="+osName,
		"ZEDRUN_ARCH="+arch,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	installed := filepath.Join(prefix, "zedrun-go")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("binary not installed: %v\n%s", err, out)
	}
	verOut, err := exec.Command(installed, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(verOut), "9.9.9") {
		t.Fatalf("installed binary reports %q", verOut)
	}

	uninstall := exec.Command("sh", script, "--uninstall")
	uninstall.Env = append(os.Environ(), "ZEDRUN_HOME="+prefix)
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatal("uninstall left the binary behind")
	}
}

func TestInstallShRejectsUnrunnableBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix installer")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zedrun-go-linux-amd64"), []byte("not a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(dir, "bin")
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "install.sh"))
	cmd.Env = append(os.Environ(),
		"ZEDRUN_BASE_URL=file://"+dir, "ZEDRUN_HOME="+prefix,
		"ZEDRUN_OS=linux", "ZEDRUN_ARCH=amd64")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("install accepted a binary that does not run:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(prefix, "zedrun-go")); !os.IsNotExist(err) {
		t.Fatal("an unrunnable binary was installed")
	}
}

func TestPowerShellInstallerParses(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("no pwsh on this machine")
	}
	script := filepath.Join(repoRoot(t), "install.ps1")
	cmd := exec.Command(pwsh, "-NoProfile", "-Command",
		"$errors = $null; "+
			"$null = [System.Management.Automation.Language.Parser]::ParseFile('"+script+"', [ref]$null, [ref]$errors); "+
			"if ($errors.Count -gt 0) { $errors | Out-String | Write-Error; exit 1 }")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.ps1 does not parse: %v\n%s", err, out)
	}
}

func TestReleaseWorkflowCoversEveryPlatform(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"linux", "darwin", "windows", "amd64", "arm64", "CGO_ENABLED"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("release.yml is missing %q", want)
		}
	}
}

func TestReadmeDocumentsOneLineInstall(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install.sh", "install.ps1", "irm", "curl"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("README is missing %q", want)
		}
	}
}
