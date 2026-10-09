package auth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// NoncedStampsRelease is the first release whose nodes sign the nonced
// stamps (the v3 coordination stamp, the nonced ACME and node-api stamps). A
// verifier accepts the older, replayable forms only while the cluster may still
// hold a node older than this (LegacyFloor).
const NoncedStampsRelease = "0.3.1"

const (
	// legacyFloorTTL is how long a reading of the registry is used. A rolling
	// upgrade moves the floor one node at a time; half a minute is far inside
	// that, and keeps every stamp verification off the database.
	legacyFloorTTL = 30 * time.Second
	// legacyFloorReadBudget bounds one read of the registry.
	legacyFloorReadBudget = 5 * time.Second
)

// LegacyFloor decides whether the older stamps are still accepted, and are still
// written, from the versions the cluster's nodes report.
//
// The nonced stamps cannot be forced on every request while some node signs
// only the older ones, but accepting the older ones beside them lets anyone who
// captured a request strip the nonce and replay it on the older form. A
// verifier therefore refuses the older form for a sender it knows speaks the
// newer: here, as soon as every node the registry holds is at
// NoncedStampsRelease or later. The versions are the nodes' own report, so a
// node can only hold the floor down (keeping the older forms accepted, as
// before), never lower the protection by lying upward.
type LegacyFloor struct {
	// Read returns the version every non-retired node of the cluster reports,
	// "" for a node that has not said.
	Read func(ctx context.Context) ([]string, error)
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
	versions, err := f.Read(ctx)
	f.at = now()
	if err != nil {
		if f.Logf != nil {
			f.Logf("could not read the nodes' versions to decide whether the older inter-node stamps are still accepted; keeping the last answer (%v, none yet means accepted): %v", f.accepts, err)
		}
		if !f.known {
			f.accepts = true
		}
		f.known = true
		return f.accepts
	}
	f.accepts, f.known = anyBelow(versions, NoncedStampsRelease), true
	return f.accepts
}

// anyBelow reports whether some version is older than min, or is not a version
// at all (a node that has not said is an old node). No version at all is a
// cluster with nothing old in it.
func anyBelow(versions []string, min string) bool {
	for _, v := range versions {
		if !versionAtLeast(v, min) {
			return true
		}
	}
	return false
}

// versionAtLeast compares dotted numbers, ignoring a leading v and a suffix
// that starts with - or +. A version that does not parse is not at least
// anything.
func versionAtLeast(v, min string) bool {
	have, ok := dotted(v)
	if !ok {
		return false
	}
	want, ok := dotted(min)
	if !ok {
		return false
	}
	for i := 0; i < len(have) || i < len(want); i++ {
		var h, w int
		if i < len(have) {
			h = have[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if h != w {
			return h > w
		}
	}
	return true
}

func dotted(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// VersionQuerier is the part of the cluster registry the floor reads: the
// ORM client satisfies it.
type VersionQuerier interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
}

// nodeVersionsSQL lists what every registered, not retired, node reports.
const nodeVersionsSQL = `SELECT node_version FROM dns_nodes WHERE last_seen <> ?`

// RegistryLegacyFloor is the floor over the versions the registry's dns_nodes
// hold. logf receives a reading that failed.
func RegistryLegacyFloor(db VersionQuerier, logf func(format string, args ...any)) *LegacyFloor {
	return &LegacyFloor{
		Logf: logf,
		Read: func(ctx context.Context) ([]string, error) {
			var rows []struct {
				Version string `db:"node_version"`
			}
			if err := db.Query(ctx, &rows, nodeVersionsSQL, constants.RetiredNodeLastSeen); err != nil {
				return nil, fmt.Errorf("read the nodes' versions: %w", err)
			}
			out := make([]string, len(rows))
			for i, row := range rows {
				out[i] = row.Version
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
