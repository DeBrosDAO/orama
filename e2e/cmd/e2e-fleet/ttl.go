package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// ttlMargin covers what the stage plan does not bound: provisioning, the
// labelling re-runs, collecting artifacts and the teardown.
const ttlMargin = 3 * time.Hour

// runTTL is the run's e2e-ttl label: the stage plan's worst case plus
// ttlMargin, or E2E_TTL when set, which may not be shorter than that. It is
// put in this process's environment (E2E_TTL) so the broker child labels
// the extras it creates the same way.
func runTTL(lay layout, lookup func(string) (string, bool)) (time.Duration, error) {
	steps, err := planStages(lay)
	if err != nil {
		return 0, err
	}
	planned := stages.WorstCase(steps) + ttlMargin
	ttl := planned
	if v, ok := lookup(provision.EnvTTL); ok && strings.TrimSpace(v) != "" {
		if ttl, err = time.ParseDuration(strings.TrimSpace(v)); err != nil || ttl <= 0 {
			return 0, fmt.Errorf("%s=%q must be a positive Go duration", provision.EnvTTL, v)
		}
		if ttl < planned {
			return 0, fmt.Errorf("%s=%s is shorter than the stage plan's worst case plus margin (%s): the orphan sweep could delete the run while it runs",
				provision.EnvTTL, ttl, planned)
		}
	}
	if err := os.Setenv(provision.EnvTTL, ttl.String()); err != nil {
		return 0, fmt.Errorf("failed to set %s for the broker child: %w", provision.EnvTTL, err)
	}
	return ttl, nil
}
