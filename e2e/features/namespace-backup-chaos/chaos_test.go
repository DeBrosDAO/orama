//go:build e2e_fleet

package namespacebackupchaos

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/rqlitelog"
)

const (
	// electionBudget bounds a namespace rqlite electing a new leader and the
	// gateway serving a backup again.
	electionBudget = 3 * time.Minute
	// webrtcEnable gives the namespace a stored secret (its TURN secret,
	// core/pkg/secrets NamespaceColumns namespace_webrtc_config).
	pathWebRTCEnable  = "/v1/namespace/webrtc/enable"
	pathWebRTCDisable = "/v1/namespace/webrtc/disable"
)

// leader finds the node the namespace's rqlite most recently named leader.
// Every member's store logs "node <id> at <raft addr> is now Leader" when the
// leader changes; the newest such line across the members names the current
// one by its overlay address. The journal is the only place that says so
// without reading the rqlite credentials, which must not reach evidence.
func leader(t testing.TB, f *fleet.Fleet, n *ns.Namespace) fleet.Node {
	t.Helper()
	members := tenancy.Members(t, f, n.Name)
	var addr string
	var newest float64
	for _, node := range members {
		out := f.Exec(t, node, "journalctl -u "+tenancy.UnitRQLite(n.Name)+" -o short-unix --no-pager | grep '"+rqlitelog.LeaderLine+"' | tail -1").Stdout
		at, leaderAddr, ok := rqlitelog.ParseLeaderLine(out)
		if ok && at > newest {
			newest, addr = at, leaderAddr
		}
	}
	if newest == 0 {
		t.Fatalf("no member of %s logged %q", n.Name, rqlitelog.LeaderLine)
	}
	for _, node := range members {
		if node.WGIP == addr {
			return node
		}
	}
	t.Fatalf("%s's newest leader, %s, is none of its members", n.Name, addr)
	return fleet.Node{}
}

// TestBackupChaos_duringLeaderChange: a backup taken while the namespace's
// rqlite leader crashes either completes with a backup that opens, or fails
// cleanly; once a new leader is elected a backup completes and restores.
func TestBackupChaos_duringLeaderChange(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	tenancy.NotesSeed(t, n, "before-crash")
	k := tenancy.NewBackupKey(t)
	victim := leader(t, f, n)
	done := make(chan *gw.Response, 1)
	go func() {
		r, _ := n.Client.JSON(t.Context(), http.MethodPost, tenancy.PathBackup, n.Owner.Token(), map[string]string{"public_key": k.PubHex}, nil)
		done <- r
	}()
	f.Kill(t, victim, tenancy.UnitRQLite(n.Name))
	if r := <-done; r != nil && r.Status == http.StatusOK {
		k.Open(t, r.Body)
	} else if r != nil && r.Status < http.StatusInternalServerError {
		t.Errorf("a backup cut by a leader crash answered %d, want 200 or a 5xx", r.Status)
	}
	var sealed []byte
	eventually.Require(t, pollEvery, electionBudget, "a backup after the new election", func() (bool, error) {
		r, err := n.Client.JSON(t.Context(), http.MethodPost, tenancy.PathBackup, n.Owner.Token(), map[string]string{"public_key": k.PubHex}, nil)
		if err != nil {
			return false, err
		}
		sealed = r.Body
		return true, nil
	})
	p := k.Open(t, sealed)
	tenancy.RestoreHTTP(t, n, tenancy.Owner(n), tenancy.RestoreBody(t, p, tenancy.RestoreKey(t, n))).Expect(t, http.StatusOK)
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"before-crash"}) {
		t.Fatalf("after the post-election restore the rows are %v", got)
	}
}

// TestBackupChaos_restoreKeyFollowsRotation: rotating the cluster's encryption
// root changes every namespace's restore key; a restore whose secrets were
// sealed to the old key is refused with nothing written, and one sealed to
// the new key succeeds (docs/CLI_REFERENCE.md "orama namespace restore-key",
// "orama maint operator rotate-secrets"). It runs on an eval cluster of its own:
// the rotation cannot be undone.
func TestBackupChaos_restoreKeyFollowsRotation(t *testing.T) {
	ev, cli := rotationCluster(t)
	n := ns.New(t, ev, ns.Options{})
	tenancy.NotesSeed(t, n, "kept")
	tenancy.Post(t, n.Client, pathWebRTCEnable, tenancy.Owner(n), nil).Expect(t, http.StatusOK)
	t.Cleanup(func() {
		tenancy.Restore(t, n.Client, http.MethodPost, pathWebRTCDisable, tenancy.Owner(n), nil, http.StatusOK)
	})
	k := tenancy.NewBackupKey(t)
	p := k.Open(t, tenancy.BackupHTTP(t, n, k))
	if len(p.Secrets) == 0 {
		t.Fatal("a namespace with WebRTC enabled backed up no secret")
	}
	oldKey := tenancy.RestoreKey(t, n)
	cli.MustOK(t, "maint", "operator", "rotate-secrets", "--rotate")
	var rotated [32]byte
	eventually.Require(t, pollEvery, electionBudget, "the restore key to follow the new root", func() (bool, error) {
		rotated = tenancy.RestoreKey(t, n)
		if rotated == oldKey {
			return false, fmt.Errorf("still the old key")
		}
		return true, nil
	})
	tenancy.NotesInsert(t, n, "after-backup")
	tenancy.RestoreHTTP(t, n, tenancy.Owner(n), tenancy.RestoreBody(t, p, oldKey)).Expect(t, http.StatusBadRequest)
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"after-backup", "kept"}) {
		t.Fatalf("a restore sealed to the old key changed the database to %v", got)
	}
	tenancy.RestoreHTTP(t, n, tenancy.Owner(n), tenancy.RestoreBody(t, p, rotated)).Expect(t, http.StatusOK)
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("after the restore under the new key the rows are %v", got)
	}
}
