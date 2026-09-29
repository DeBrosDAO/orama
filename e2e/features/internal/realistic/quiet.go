//go:build e2e_fleet

package realistic

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Quiet is a client of base with the run's pinned trust and credential
// pacing that records no evidence. Sustained load makes one request a second
// per worker for the whole soak: recording each would bury the evidence file
// under hundreds of megabytes the report never shows. Load tests record
// their numbers as artifacts instead (WriteJSON), and every setup and
// assertion request still goes through a recording client.
func Quiet(t testing.TB, f *fleet.Fleet, base string) *gw.Client {
	t.Helper()
	c, err := gw.New(base, f.State.CAFile, nil)
	if err != nil {
		t.Fatalf("failed to build a load client for %s: %v", base, err)
	}
	return c
}
