package toolchain

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// VerifyResult is evidence, not opinion: it only passes when a fresh probe
// found a usable install that a bare command resolves to.
type VerifyResult struct {
	Tool    string `json:"tool"`
	OK      bool   `json:"ok"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail"`
}

// Verify judges a freshly collected Status. It never trusts a claim that the
// fix worked; the caller must pass a Status detected after the fix.
func Verify(st Status, d Diagnosis) VerifyResult {
	r := VerifyResult{Tool: st.Tool, Kind: d.Kind, Detail: d.Symptom}
	if d.Kind == KindOK && st.Active != nil {
		r.OK = true
		r.Version, r.Path = st.Active.Version, st.Active.Path
	}
	return r
}

// JSON writes v as indented JSON followed by a newline, the format an
// assistant parses.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// RenderStatus prints a human-readable status.
func RenderStatus(w io.Writer, st Status, d Diagnosis) {
	fmt.Fprintf(w, "%s on %s", st.Tool, st.OS)
	if st.Shell != ShellUnknown {
		fmt.Fprintf(w, " (%s)", st.Shell)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  result      %s\n", d.Kind)
	fmt.Fprintf(w, "  symptom     %s\n", d.Symptom)
	if d.Cause != "" {
		fmt.Fprintf(w, "  cause       %s\n", d.Cause)
	}
	if st.Active != nil {
		fmt.Fprintf(w, "  resolves to %s  %s\n", st.Active.Path, st.Active.Version)
	}
	for _, in := range st.Installs {
		flags := []string{}
		if !in.OK {
			flags = append(flags, "unusable")
		}
		if !in.OnPath {
			flags = append(flags, "not on PATH")
		}
		if in.Shim {
			flags = append(flags, "shim")
		}
		fmt.Fprintf(w, "  install     %s  %s %s\n", in.Path, in.Version, bracket(flags))
	}
	for _, sh := range st.Shadowed {
		fmt.Fprintf(w, "  shadowed    %s hides %s\n", sh.Winner.Path, sh.Loser.Path)
	}
	if st.Route != nil {
		fmt.Fprintf(w, "  route       %s\n", st.Route.Command())
	} else if st.ManagerMissing {
		fmt.Fprintln(w, "  route       none: no package manager for this tool is installed")
	}
	if st.Elevation.Needed {
		fmt.Fprintf(w, "  elevation   needed; zedrun-go can run it: %t\n", st.Elevation.CanRun)
	}
}

func bracket(f []string) string {
	if len(f) == 0 {
		return ""
	}
	return "[" + strings.Join(f, ", ") + "]"
}

// RenderPlan prints a plan as copy-paste-safe blocks: every command sits alone
// on its own line inside a fence, with no prompt character and no decoration,
// so it survives being pasted or relayed through a chat window unchanged.
func RenderPlan(w io.Writer, p Plan) {
	fmt.Fprintf(w, "%s: %s\n", p.Tool, p.Summary)
	if p.Cause != "" && p.Kind != KindOK {
		fmt.Fprintf(w, "cause: %s\n", p.Cause)
	}
	for i, s := range p.Steps {
		fmt.Fprintf(w, "\nstep %d: %s\n", i+1, s.Title)
		if s.File != "" {
			fmt.Fprintf(w, "file: %s\n", s.File)
		}
		fmt.Fprintf(w, "```\n%s\n```\n", s.Command)
		if s.Note != "" {
			fmt.Fprintf(w, "%s\n", s.Note)
		}
		if s.Explain {
			fmt.Fprintln(w, "(zedrun-go will not run this one for you)")
		}
	}
	if len(p.Verify) > 0 && p.Kind != KindOK {
		fmt.Fprintln(w, "\nverify in a NEW terminal:")
		fmt.Fprintf(w, "```\n%s\n```\n", strings.Join(p.Verify, "\n"))
	}
	if p.Confirm != "" {
		fmt.Fprintf(w, "\n%s\n", p.Confirm)
	}
	if p.Stop != "" {
		fmt.Fprintf(w, "\nstop: %s\n", p.Stop)
	}
}
