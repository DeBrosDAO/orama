package namespace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// fakeIndexRQLite records what EnsureRQLite asked of its seams.
type fakeIndexRQLite struct {
	hasState    bool
	stateErr    error
	identity    rqlite.RaftIdentity
	identityErr error

	stateAuthFile string
	identityState *bool
	spawned       *rqlite.InstanceConfig
}

func (f *fakeIndexRQLite) supervisor(oramaDir string) *IndexSupervisor {
	return &IndexSupervisor{
		oramaDir: oramaDir,
		dataDir:  filepath.Join(oramaDir, "data"),
		logger:   zap.NewNop(),
		raftState: func(_ context.Context, _, _, authFile string) (bool, error) {
			f.stateAuthFile = authFile
			return f.hasState, f.stateErr
		},
		raftIdentity: func(_, _, _ string, hasState bool) (rqlite.RaftIdentity, error) {
			f.identityState = &hasState
			return f.identity, f.identityErr
		},
		spawnRQLite: func(_ context.Context, _, _ string, cfg rqlite.InstanceConfig) error {
			f.spawned = &cfg
			return nil
		},
	}
}

const (
	testJoin    = "10.0.0.1:10101"
	testHTTPAdv = "10.0.0.2:10100"
	testRaftAdv = "10.0.0.2:10101"
)

// raft state × join address. A member restarts into the cluster it has and is
// never handed -join (every restart would depend on that one address); a node
// without state joins the address it was given; without either it is the
// genesis node and bootstraps. The same state reaches ResolveRaftIdentity,
// which picks the raft id from it.
func TestEnsureRQLite_joinDecisionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hasState bool
		join     string
		wantJoin []string
	}{
		{"member with a join address restarts without -join", true, testJoin, nil},
		{"member without a join address restarts", true, "", nil},
		{"new node joins the address it was given", false, testJoin, []string{testJoin}},
		{"genesis node bootstraps", false, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeIndexRQLite{hasState: tc.hasState}
			if err := f.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "node-1", "12D3Koo", testHTTPAdv, testRaftAdv, tc.join, ""); err != nil {
				t.Fatal(err)
			}
			if f.spawned == nil {
				t.Fatal("rqlite@index was not spawned")
			}
			if !reflect.DeepEqual(f.spawned.JoinAddresses, tc.wantJoin) {
				t.Errorf("JoinAddresses = %v, want %v", f.spawned.JoinAddresses, tc.wantJoin)
			}
			if f.identityState == nil || *f.identityState != tc.hasState {
				t.Errorf("ResolveRaftIdentity got hasState=%v, want %v", f.identityState, tc.hasState)
			}
		})
	}
}

// The raft state is read from the core raft directory, with its auth file.
func TestEnsureRQLite_readsTheCoreRaftDirectory(t *testing.T) {
	f := &fakeIndexRQLite{}
	oramaDir := t.TempDir()
	s := f.supervisor(oramaDir)
	if err := s.EnsureRQLite(context.Background(), "node-1", "", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	if f.spawned.DataDir != s.CoreRQLiteDir() {
		t.Errorf("DataDir = %s, want %s", f.spawned.DataDir, s.CoreRQLiteDir())
	}
	if want := filepath.Join(s.CoreRQLiteDir(), rqlite.AuthFileName); f.stateAuthFile != want {
		t.Errorf("auth file = %s, want %s", f.stateAuthFile, want)
	}
}

// The resolved raft id is passed as -node-id; an empty one passes nothing.
func TestEnsureRQLite_raftIdentityBecomesNodeIDArg(t *testing.T) {
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: "12D3Koo", Migrated: true}}
	if err := f.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "node-1", "12D3Koo", testHTTPAdv, testRaftAdv, "", "-raft-timeout 5s"); err != nil {
		t.Fatal(err)
	}
	if f.spawned.ExtraArgs != "-raft-timeout 5s -node-id 12D3Koo" {
		t.Errorf("ExtraArgs = %q", f.spawned.ExtraArgs)
	}

	g := &fakeIndexRQLite{hasState: true}
	if err := g.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "node-1", "", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.spawned.ExtraArgs, "-node-id") {
		t.Errorf("an empty identity must pass no -node-id: %q", g.spawned.ExtraArgs)
	}
}

// Not knowing whether this node is a member is not "no": joining with state,
// or bootstrapping as a member, creates a duplicate voter or a second cluster.
func TestEnsureRQLite_unknownStateOrIdentityStartsNothing(t *testing.T) {
	for name, f := range map[string]*fakeIndexRQLite{
		"raft state unreadable":   {stateErr: errors.New("rqlited is running and does not answer")},
		"identity not resolvable": {identityErr: errors.New("marker unreadable")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := f.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "node-1", "12D3Koo", testHTTPAdv, testRaftAdv, testJoin, ""); err == nil {
				t.Fatal("expected an error")
			}
			if f.spawned != nil {
				t.Error("rqlite@index was started on an unknown state")
			}
		})
	}
}

// A supervisor built for a node reads the real disk and starts the real unit.
func TestNewIndexSupervisor_wiresTheRealRQLiteSeams(t *testing.T) {
	s := NewIndexSupervisor(t.TempDir(), nil)
	if s.raftState == nil || s.raftIdentity == nil || s.spawnRQLite == nil {
		t.Fatal("NewIndexSupervisor left an rqlite seam unset")
	}
}
