//go:build e2e_fleet

package rqliteraft

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// raftStatus is the part of rqlite's /status the tests read.
type raftStatus struct {
	Store struct {
		NodeID string `json:"node_id"`
		Leader struct {
			NodeID string `json:"node_id"`
			Addr   string `json:"addr"`
		} `json:"leader"`
		Raft struct {
			State string `json:"state"`
		} `json:"raft"`
	} `json:"store"`
}

// TestRQLite_raftIdentityMarkers: beside raft.db each node records the raft
// id it started with (its peer id), the raft address it was last confirmed a
// member at (its WireGuard address on 10101) and its suffrage
// (core/pkg/rqlite/identity.go; docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama maint node migrate-raft-id").
func TestRQLite_raftIdentityMarkers(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	for _, n := range f.State.Nodes {
		entry, err := infra.ReportFor(r, n)
		if err != nil {
			t.Fatal(err)
		}
		read := func(name string) string {
			return strings.TrimSpace(string(f.ReadFile(t, n, infra.CoreRQLiteDir+"/"+name)))
		}
		if id := read("raft-node-id"); id != entry.Report.RQLite.NodeID {
			t.Errorf("%s: raft-node-id %q, rqlite runs as %q", n.Name, id, entry.Report.RQLite.NodeID)
		}
		if addr, want := read("raft-adv-addr"), fmt.Sprintf("%s:%d", entry.Report.WGIP, infra.IndexRQLiteRaft); addr != want {
			t.Errorf("%s: raft-adv-addr %q, want %q", n.Name, addr, want)
		}
		if s := read("raft-suffrage"); s != "Voter" {
			t.Errorf("%s: raft-suffrage %q, want Voter", n.Name, s)
		}
		for _, m := range []string{"raft-node-id", "raft-adv-addr", "raft-suffrage"} {
			if st, ok := infra.StatFile(t, f, n, infra.CoreRQLiteDir+"/"+m); !ok || st.Owner != "orama" {
				t.Errorf("%s: %s is %+v, want owned by orama", n.Name, m, st)
			}
		}
	}
}

// TestRQLite_everyNodeAgreesOnTheLeader: each node's own /status names the
// same leader, and exactly one node says it is the leader.
func TestRQLite_everyNodeAgreesOnTheLeader(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	leaders := map[string]int{}
	selfLeaders := 0
	for _, n := range f.State.Nodes {
		var s raftStatus
		if err := json.Unmarshal([]byte(f.MustExec(t, n, infra.IndexStatusCurl()).Stdout), &s); err != nil {
			t.Fatalf("%s: /status is not JSON: %v", n.Name, err)
		}
		leaders[s.Store.Leader.NodeID]++
		if s.Store.Raft.State == monitor.RaftLeader {
			selfLeaders++
		}
	}
	if len(leaders) != 1 || selfLeaders != 1 {
		t.Fatalf("leaders named %v, %d nodes say they lead", leaders, selfLeaders)
	}
}

// TestRQLite_readConsistencyLevels: a read of replicated data answers the
// same at every consistency level on every node (none, weak, strong,
// linearizable).
func TestRQLite_readConsistencyLevels(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	const sql = "SELECT node_id FROM node_credentials ORDER BY node_id"
	var want string
	for _, n := range f.State.Nodes {
		for _, level := range []string{"none", "weak", "strong", "linearizable"} {
			q, err := infra.IndexQueryAt(t, f, n, level, sql)
			if err != nil {
				t.Errorf("%s at %s: %v", n.Name, level, err)
				continue
			}
			got := fmt.Sprint(q.Values)
			if want == "" {
				want = got
			}
			if got != want {
				t.Errorf("%s at %s read %s, want %s", n.Name, level, got, want)
			}
		}
	}
}

// TestSchema_inSyncEverywhere: every node's local schema is at the version
// its binary requires (`orama maint node schema status`), `schema apply` on an
// up-to-date database has nothing to do, and /v1/schema-status says so to any
// credential and refuses none (docs/whitepaper/technical-reference/appendices/i-api-surface.md "/v1/schema-status").
func TestSchema_inSyncEverywhere(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		st := infra.OnNode(t, f, n, "maint", "node", "schema", "status")
		if st.Exit != 0 || !strings.Contains(st.Stdout, "up to date") {
			t.Errorf("%s: schema status exit %d:\n%s%s", n.Name, st.Exit, f.Redact(st.Stdout), f.Redact(st.Stderr))
		}
		ap := infra.OnNode(t, f, n, "maint", "node", "schema", "apply", "--yes")
		if ap.Exit != 0 || !strings.Contains(ap.Stdout, "No pending migrations") {
			t.Errorf("%s: schema apply on an up-to-date database: exit %d:\n%s", n.Name, ap.Exit, f.Redact(ap.Stdout))
		}
	}
	c := harness.GW(t)
	if r := c.MustSend(t, gw.Req{Path: "/v1/schema-status"}); r.Status != http.StatusUnauthorized {
		t.Errorf("schema-status with no credential: HTTP %d, want 401", r.Status)
	}
	u := gw.NewUser(t, f, gw.LobbyNamespace)
	var s struct {
		OK       bool `json:"ok"`
		InSync   bool `json:"in_sync"`
		Required int  `json:"required_version"`
		Applied  int  `json:"applied_version"`
		Pending  []any
	}
	if err := c.MustSend(t, gw.Req{Path: "/v1/schema-status", Bearer: u.Token()}).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if !s.OK || !s.InSync || s.Required != s.Applied || len(s.Pending) != 0 {
		t.Errorf("schema-status: %+v", s)
	}
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/schema-status", Bearer: u.Token()}); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST schema-status: HTTP %d, want 405", r.Status)
	}
}

// TestOperatorHealth_operatorsOnly: the detailed health is for operators: no
// credential is 401, a signed-in wallet that operates nothing is 403
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md: the detail is /v1/operator/health's).
func TestOperatorHealth_operatorsOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	if r := c.MustSend(t, gw.Req{Path: "/v1/operator/health"}); r.Status != http.StatusUnauthorized {
		t.Errorf("no credential: HTTP %d, want 401", r.Status)
	}
	u := gw.NewUser(t, f, gw.LobbyNamespace)
	if r := c.MustSend(t, gw.Req{Path: "/v1/operator/health", Bearer: u.Token()}); r.Status != http.StatusForbidden {
		t.Errorf("a non-operator: HTTP %d, want 403: %.200s", r.Status, r.Body)
	}
}
