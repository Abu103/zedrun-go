package toolchain

import (
	"context"
	"sort"

	"github.com/zedrun/zedrun-go/internal/catalog"
	"github.com/zedrun/zedrun-go/internal/config"
	"github.com/zedrun/zedrun-go/internal/detect"
)

// Report pairs one tool's Status with the Diagnosis made from it.
type Report struct {
	Status    Status    `json:"status"`
	Diagnosis Diagnosis `json:"diagnosis"`
}

// Scan diagnoses every toolchain that has at least one install on this
// machine. A tool with nothing installed is not "unhealthy", it is absent, so
// it is left out. When only is non-nil, the scan is limited to those ids.
func Scan(ctx context.Context, cfg config.Config, cache *detect.ProbeCache, env map[string]string, osName string, isRoot bool, only []string) []Report {
	ids := only
	if len(ids) == 0 {
		for id := range catalog.Tools {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []Report
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		st, err := Detect(ctx, cfg, cache, env, osName, id, isRoot)
		if err != nil || len(st.Installs) == 0 {
			continue
		}
		out = append(out, Report{Status: st, Diagnosis: Diagnose(st, Options{Shell: st.Shell})})
	}
	return out
}

// Unhealthy keeps the reports whose diagnosis is not KindOK.
func Unhealthy(reports []Report) []Report {
	var bad []Report
	for _, r := range reports {
		if r.Diagnosis.Kind != KindOK {
			bad = append(bad, r)
		}
	}
	return bad
}
