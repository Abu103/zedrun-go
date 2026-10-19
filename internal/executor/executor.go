// Package executor runs a planner.Plan: it writes any wrapper scripts, puts the
// toolchain's directory first on PATH, runs each step in order, and stops at the
// first failure so a stale binary is never run.
package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/platform"
	"github.com/zedrun/zedrun-go/internal/planner"
)

// Execute runs every step in the plan and returns the exit code the caller
// should exit with: the failing step's code, or the last step's code.
func Execute(p planner.Plan, cfg config.Config, out io.Writer, timeout time.Duration, echo, verbose bool) int {
	if err := writeScripts(p); err != nil {
		fmt.Fprintf(out, "zedrun-go: %v\n", err)
		return 1
	}
	if timeout <= 0 && cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout * float64(time.Second))
	}
	env := buildEnv(p, cfg)
	osName := platform.OSName("")
	start := time.Now()

	last := 0
	for _, st := range p.Steps {
		if echo || verbose {
			fmt.Fprintf(out, "> %s\n", strings.Join(st.Argv, " "))
		}
		code, err := runStep(st, env, out, timeout)
		if err != nil {
			fmt.Fprintf(out, "zedrun-go: %v\n", err)
			return 1
		}
		last = code
		if code != 0 {
			if cfg.ShowFooter {
				printFooter(out, code, time.Since(start).Seconds(), osName)
			}
			return code
		}
	}
	if cfg.ShowFooter {
		printFooter(out, last, time.Since(start).Seconds(), osName)
	}
	return last
}

func printFooter(out io.Writer, code int, elapsed float64, osName string) {
	msg := fmt.Sprintf("[Done] exit code %d in %.2fs", code, elapsed)
	if d := DescribeExit(code, osName); d != "" {
		msg += " — " + d
	}
	fmt.Fprintln(out, msg)
}

func writeScripts(p planner.Plan) error {
	for path, content := range p.Scripts {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func runStep(st planner.Step, env []string, out io.Writer, timeout time.Duration) (int, error) {
	if len(st.Argv) == 0 {
		return 0, nil
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, st.Argv[0], st.Argv[1:]...)
	cmd.Dir = st.Cwd
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = time.Second

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return 124, nil
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 1, err
}

func buildEnv(p planner.Plan, cfg config.Config) []string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range cfg.Env {
		m[k] = v
	}
	for k, v := range p.Env {
		m[k] = v
	}
	if len(p.PathPrefix) > 0 {
		parts := append([]string{}, p.PathPrefix...)
		if cur := m["PATH"]; cur != "" {
			parts = append(parts, cur)
		}
		m["PATH"] = strings.Join(parts, string(os.PathListSeparator))
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// DescribeExit explains a nonzero exit code. A negative code is a signal on
// POSIX and an NTSTATUS on Windows; the sign check must come first, because
// masking a negative Go int as uint32 would also match an NTSTATUS pattern.
func DescribeExit(code int, osName string) string {
	switch {
	case code == 0:
		return ""
	case code == 124:
		return "timed out"
	case code < 0 && osName == "windows":
		u := uint32(int32(code))
		switch u {
		case 0xC0000135:
			return "0xC0000135: a required DLL was not found (add its directory to PATH)"
		case 0xC0000005:
			return "0xC0000005: access violation"
		case 0xC0000142:
			return "0xC0000142: DLL initialisation failed"
		default:
			return fmt.Sprintf("NTSTATUS 0x%08X", u)
		}
	case code < 0:
		return fmt.Sprintf("killed by signal %d%s", -code, signalName(-code))
	default:
		return fmt.Sprintf("exit code %d", code)
	}
}

func signalName(sig int) string {
	switch sig {
	case 2:
		return " (SIGINT)"
	case 6:
		return " (SIGABRT)"
	case 9:
		return " (SIGKILL)"
	case 11:
		return " (SIGSEGV)"
	case 13:
		return " (SIGPIPE)"
	case 15:
		return " (SIGTERM)"
	}
	return ""
}
