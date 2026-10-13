package updateagent

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

func TestRaftView_readsTheLeaderAndCountsReachableVoters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			fmt.Fprint(w, `{"store":{"leader":{"node_id":"n1","addr":"10.0.0.1:7002"},"raft":{"state":"Follower"}}}`)
		case "/nodes":
			fmt.Fprint(w, `{"nodes":[
				{"id":"n1","addr":"10.0.0.1:7002","voter":true,"reachable":true,"leader":true},
				{"id":"n2","addr":"10.0.0.2:7002","voter":true,"reachable":false},
				{"id":"n3","addr":"10.0.0.3:7002","voter":true,"reachable":true},
				{"id":"n4","addr":"10.0.0.4:7002","voter":false,"reachable":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	view, err := raftView{admin: rqlite.NewAdminClient(srv.URL, "u", "p")}.View(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view != (autoupdate.RaftView{LeaderHost: "10.0.0.1", Voters: 3, HealthyVoters: 2}) {
		t.Fatalf("view = %+v: a non-voter counts as neither, and an unreachable voter is not healthy", view)
	}
}

func TestRaftView_anUnansweringRqliteIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if _, err := (raftView{admin: rqlite.NewAdminClient(srv.URL, "u", "p")}).View(t.Context()); err == nil {
		t.Fatal("a raft configuration that could not be read was reported")
	}
}

func TestLeaderHost(t *testing.T) {
	var s rqlite.RQLiteStatus
	if got := leaderHost(&s); got != "" {
		t.Errorf("no leader: %q", got)
	}
	s.Store.Raft.LeaderAddr = "10.0.0.7:7002"
	if got := leaderHost(&s); got != "10.0.0.7" {
		t.Errorf("the raft leader address: %q", got)
	}
	s.Store.Leader.Addr = "10.0.0.9:7002"
	if got := leaderHost(&s); got != "10.0.0.9" {
		t.Errorf("the leader field wins: %q", got)
	}
}

func TestNodeRole_aMachineThatRunsTheChainIsAValidator(t *testing.T) {
	dir := t.TempDir()
	role, err := roleIn(dir)
	if err != nil || role != autoupdate.RoleCluster {
		t.Fatalf("a cluster node: %q, %v", role, err)
	}
	chain := filepath.Join(dir, install.GlobalServiceUnit(install.GlobalServiceOrder[0]))
	if err := os.WriteFile(chain, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if role, err := roleIn(dir); err != nil || role != autoupdate.RoleValidator {
		t.Fatalf("a chain host: %q, %v", role, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := roleIn(file); err == nil {
		t.Fatal("a unit directory that cannot be read decided a role")
	}
}

// fakeCLI makes installedCLI a script that exits with code.
func fakeCLI(t *testing.T, code int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama")
	script := fmt.Sprintf("#!/bin/sh\nexit %d\n", code)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := installedCLI
	installedCLI = path
	t.Cleanup(func() { installedCLI = prev })
}

func TestMachineUpgrade_exitCodes(t *testing.T) {
	m := &machine{}
	fakeCLI(t, 0)
	if err := m.Upgrade(t.Context()); err != nil {
		t.Fatalf("a successful upgrade: %v", err)
	}
	fakeCLI(t, 8)
	if err := m.Upgrade(t.Context()); !errors.Is(err, autoupdate.ErrNotStarted) {
		t.Fatalf("a preflight refusal: %v, want ErrNotStarted", err)
	}
	fakeCLI(t, 1)
	if err := m.Upgrade(t.Context()); err == nil || errors.Is(err, autoupdate.ErrNotStarted) {
		t.Fatalf("an upgrade that failed after stopping services: %v", err)
	}
	installedCLI = filepath.Join(t.TempDir(), "missing")
	if err := m.Upgrade(t.Context()); !errors.Is(err, autoupdate.ErrNotStarted) {
		t.Fatalf("a CLI that cannot be started changed nothing: %v", err)
	}
}
