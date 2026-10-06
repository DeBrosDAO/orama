//go:build e2e_fleet

package chaos

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

const (
	// unitPoll paces the watch for a killed unit's restart.
	unitPoll = 2 * time.Second
	// raftClass is the service class whose leader is the victim.
	raftClass = "rqlite"
)

// victimFor is the node a class is killed on: rqlite on the raft leader (an
// election must follow), CoreDNS on a nameserver, everything else on a
// follower.
func victimFor(t testing.TB, f *fleet.Fleet, r *monitor.Report, sc realistic.ServiceClass) fleet.Node {
	t.Helper()
	switch {
	case sc.Name == raftClass:
		return infra.Leader(t, r)
	case sc.NameserverOnly:
		return tenancy.Nameservers(f)[0]
	default:
		return infra.Followers(t, r)[0]
	}
}

// TestChaos_killEachServiceClassRecoversByItself: SIGKILL of every process of
// each service class, one class at a time on one node. The product itself
// brings it back (a new start, before the harness would start it), the other
// nodes keep serving meanwhile, and the whole cluster converges again before
// the next class (docs/DEV_DEPLOY.md: services are supervised by
// orama-node and systemd).
func TestChaos_killEachServiceClassRecoversByItself(t *testing.T) {
	f := harness.Fleet(t)
	for _, sc := range realistic.ServiceClasses {
		t.Run(sc.Name, func(t *testing.T) {
			realistic.RequireFaultBudget(t, "killing "+sc.Name, faultWorst)
			victim := victimFor(t, f, infra.RequireHealthy(t), sc)
			if state := f.Unit(t, victim, sc.Unit); state != "active" {
				t.Fatalf("%s: %s is %q before the kill", victim.Name, sc.Unit, state)
			}
			before := tenancy.ActiveSince(t, f, victim, sc.Unit)
			f.Kill(t, victim, sc.Unit)
			requireServing(t, others(f, victim), "while "+sc.Name+" is down on "+victim.Name)
			eventually.Require(t, unitPoll, recoverBudget, sc.Unit+" restarted by itself on "+victim.Name, func() (bool, error) {
				state := f.Unit(t, victim, sc.Unit)
				since := tenancy.ActiveSince(t, f, victim, sc.Unit)
				if state == "active" && since != before {
					return true, nil
				}
				return false, fmt.Errorf("%s (started at %s, was %s)", state, since, before)
			})
			healed(t, "the cluster after "+sc.Name+" was killed on "+victim.Name)
			requireServing(t, f.State.Nodes, "after "+sc.Name+" recovered")
		})
	}
}
