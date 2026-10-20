package sources

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Real-network check, skipped unless REALNET=1: the default suite must pass on
// a machine with no network, so this is opt-in.
//
//	REALNET=1 go test ./internal/sources/ -run TestRealEndpoints -v
func TestRealEndpoints(t *testing.T) {
	if os.Getenv("REALNET") != "1" {
		t.Skip("set REALNET=1 to hit the real endpoints")
	}
	f := NewFetcher("", 5*time.Minute, nil)
	for _, id := range []string{"bash", "php", "perl", "sqlite3", "ghc", "ruby", "tsc", "swift", "lua", "r"} {
		p := Products[id]
		l := FetchLatest(p, f)
		fmt.Printf("%-8s stable=%-16q pre=%-16q src=%s\n", id, l.Stable, l.Pre, l.Source)
	}
}
