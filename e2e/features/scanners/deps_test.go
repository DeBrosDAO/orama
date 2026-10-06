//go:build e2e_fleet

package scanners

import (
	"encoding/json"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
)

// sdkPackages are the published TypeScript packages; their production
// dependencies ship to every application that installs them.
var sdkPackages = []string{"sdk", "sdk-vault"}

// auditReport is the part of `pnpm audit --json` the gate reads.
type auditReport struct {
	Metadata *struct {
		Vulnerabilities map[string]int `json:"vulnerabilities"`
	} `json:"metadata"`
}

// TestPnpmAudit_sdkProductionDependencies: no production dependency of the
// SDKs has a known advisory, at any severity. Network: pnpm audit sends the
// lockfile's dependency list to the npm registry's advisory endpoint
// (registry.npmjs.org) from the runner; nothing of the fleet is sent.
func TestPnpmAudit_sdkProductionDependencies(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "pnpm", "install pnpm to audit the SDKs' dependencies")
	s := newScan(t)
	for _, pkg := range sdkPackages {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()
			res := s.run(t, pkg, auditBudget, "pnpm", "audit", "--prod", "--json")
			var rep auditReport
			if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil || rep.Metadata == nil {
				t.Fatalf("pnpm audit in %s printed no audit report (exit %d, %v):\n%s", pkg, res.Exit, err, realistic.Tail(res.Output()))
			}
			total := 0
			for _, n := range rep.Metadata.Vulnerabilities {
				total += n
			}
			if total > 0 || res.Exit != 0 {
				t.Errorf("%s: %d advisories in production dependencies %v (exit %d)", pkg, total, rep.Metadata.Vulnerabilities, res.Exit)
			}
		})
	}
}
