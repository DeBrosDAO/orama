package auth

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The stamp protocol levels a node can report (dns_nodes.stamp_level). A level is
// what the node's build signs, stated by the node, never inferred from its
// release number: release numbers are not comparable across the network's
// version lines, and a build that predates the capability cannot report one.
const (
	// StampLevelLegacy is the default: a node that never reported, or that
	// reports it signs only the older stamps (v1 and v2 coordination, the
	// unnonced ACME and node-api stamps).
	StampLevelLegacy = 0
	// StampLevelNonced signs the v3 coordination stamp and the nonced ACME and
	// node-api stamps. A verifier accepts the older, replayable forms only while
	// the cluster may still hold a node below this level (LegacyFloor).
	StampLevelNonced = 1
	// CurrentStampLevel is the level this build signs, and so the level it
	// reports about itself.
	CurrentStampLevel = StampLevelNonced
)

const (
	// legacyFloorTTL is how long a reading of the registry is used. A rolling
	// upgrade moves the floor one node at a time; half a minute is far inside
	// that, and keeps every stamp verification off the database.
	legacyFloorTTL = 30 * time.Second
	// legacyFloorReadBudget bounds one read of the registry.
	legacyFloorReadBudget = 5 * time.Second
)

// LegacyFloor decides whether the older stamps are still accepted, and are still
// written, from the stamp levels the cluster's nodes report.
//
// The nonced stamps cannot be forced on every request while some node signs
// only the older ones, but accepting the older ones beside them lets anyone who
// captured a request strip the nonce and replay it on the older form. A
// verifier therefore refuses the older form for a sender it knows speaks the
// newer: here, as soon as every node the registry holds reports
// StampLevelNonced. A node can only hold the floor down (keeping the older
// forms accepted, as before), never lower the protection by what it reports.
//
// A level is trusted only while it is current (see nodeLevelsSQL): a node
// rolled back to a build that predates the capability keeps its last reported
// level in the registry, and that build never overwrites it, so the level is
// counted only when it was written together with the node's latest heartbeat.
type LegacyFloor struct {
	// Read returns the stamp level every non-retired node of the cluster
	// currently reports: StampLevelLegacy for a node that has not said or whose
	// report is not current.
	Read func(ctx context.Context) ([]int, error)
	// Logf records a reading that failed. It may be nil.
	Logf func(format string, args ...any)
	// Now is time.Now unless a test sets it.
	Now func() time.Time

	mu      sync.Mutex
	at      time.Time
	known   bool
	accepts bool
}

// Accepts reports whether the older stamps are still accepted. The answer is the
// last reading for legacyFloorTTL. When the registry cannot be read the last
// answer stands, and with none the older forms stay accepted (a rolling upgrade
// must not stop its own nodes from talking because the registry blinked); the
// failure is logged every time it is tried.
func (f *LegacyFloor) Accepts() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	if f.known && now().Sub(f.at) < legacyFloorTTL {
		return f.accepts
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyFloorReadBudget)
	defer cancel()
	levels, err := f.Read(ctx)
	f.at = now()
	if err != nil {
		if f.Logf != nil {
			f.Logf("could not read the nodes' stamp levels to decide whether the older inter-node stamps are still accepted; keeping the last answer (%v, none yet means accepted): %v", f.accepts, err)
		}
		if !f.known {
			f.accepts = true
		}
		f.known = true
		return f.accepts
	}
	f.accepts, f.known = anyBelow(levels, StampLevelNonced), true
	return f.accepts
}

// anyBelow reports whether some level is lower than min. No level at all is a
// cluster with nothing old in it.
func anyBelow(levels []int, min int) bool {
	for _, l := range levels {
		if l < min {
			return true
		}
	}
	return false
}

// LevelQuerier is the part of the cluster registry the floor reads: the
// ORM client satisfies it.
type LevelQuerier interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
}

// nodeLevelsSQL lists the stamp level every registered, not retired, node
// reports, as a current report only.
//
// A node writes stamp_level and stamp_level_at in the same statement as the
// last_seen of its register or heartbeat, so for a node running this code
// stamp_level_at is last_seen. A build that predates the capability refreshes
// last_seen by a statement that does not name either column, so after a
// rollback last_seen moves on and stamp_level_at stays behind: the node then
// counts as StampLevelLegacy, whatever level it last reported. ” (never
// reported) sorts before every timestamp, which gives the same answer.
var nodeLevelsSQL = fmt.Sprintf(`SELECT CASE WHEN stamp_level_at >= last_seen THEN stamp_level ELSE %d END AS stamp_level
	FROM dns_nodes WHERE last_seen <> ?`, StampLevelLegacy)

// RegistryLegacyFloor is the floor over the stamp levels the registry's
// dns_nodes hold. logf receives a reading that failed.
func RegistryLegacyFloor(db LevelQuerier, logf func(format string, args ...any)) *LegacyFloor {
	return &LegacyFloor{
		Logf: logf,
		Read: func(ctx context.Context) ([]int, error) {
			var rows []struct {
				Level int `db:"stamp_level"`
			}
			if err := db.Query(ctx, &rows, nodeLevelsSQL, constants.RetiredNodeLastSeen); err != nil {
				return nil, fmt.Errorf("read the nodes' stamp levels: %w", err)
			}
			out := make([]int, len(rows))
			for i, row := range rows {
				out[i] = row.Level
			}
			return out, nil
		},
	}
}

var installedFloor atomic.Pointer[LegacyFloor]

// InstallLegacyFloor makes f decide, process-wide, whether the older stamps
// are accepted by the verifiers and written by the signers here. A process
// that never installs one (a tool, a test) keeps both, as every build before
// the floor did. nil removes it.
func InstallLegacyFloor(f *LegacyFloor) { installedFloor.Store(f) }

// legacyStampsAccepted is the verifiers' and signers' question.
func legacyStampsAccepted() bool {
	f := installedFloor.Load()
	return f == nil || f.Accepts()
}
