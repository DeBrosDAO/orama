//go:build e2e_fleet

package referenceapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	notesApp     = "notes"
	noteWriters  = 4
	notesTotal   = 40
	notePropagat = 3 * time.Minute // a fresh pin may be invisible on a node for two minutes (storage/download_handler.go)
	teardownWait = 3 * time.Minute
)

type note struct {
	ID   string `json:"id"`
	Cid  string `json:"cid"`
	Text string `json:"text"`
}

// TestReferenceGo_notesThroughTheGatewayAsItself: the Go backend, built by
// `orama deploy go` on the runner, runs on its home node and a replica and
// stores notes in the namespace's IPFS and cache as itself. It renews its
// token and keeps working; 40 notes written by four clients at once read back
// through every node; an update and a rollback reach both replicas; deleting
// the app stops it on both (docs/DEPLOYMENT_GUIDE.md "Deploying Go Backends",
// "Updating a Deployment").
func TestReferenceGo_notesThroughTheGatewayAsItself(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "`orama deploy go` cross-compiles the backend on the deploying machine")
	tn := realistic.NewTenant(t)
	u := tn.Deploy(t, "go", realistic.ServerApp(t, tn.F, realistic.AppGo, "go-v1"), notesApp, "--env", "APP_VERSION=go-v1")
	tn.Grant(t, notesApp, roleRuntime)
	tn.EveryNodeServes(t, u, "/health", "ok")
	requireReplicas(t, tn, "go", notesApp)
	app := tn.App(u)
	var r struct {
		Changed   bool `json:"changed"`
		ExpiresIn int  `json:"expires_in"`
		Who       struct {
			Principal string   `json:"principal"`
			Role      string   `json:"role"`
			Grants    []string `json:"grants"`
		} `json:"who"`
	}
	if _, err := postJSON(t.Context(), app, "/renew", "", nil, &r); err != nil {
		t.Fatalf("the Go backend could not renew its workload token: %v", err)
	}
	checkRenewal(t, renewal{Changed: r.Changed, ExpiresIn: r.ExpiresIn, Principal: r.Who.Principal, Role: r.Who.Role, Grants: r.Who.Grants},
		roleRuntime, "storage", "cache")
	notes := writeNotes(t, tn.F, app)
	for _, node := range tn.F.State.Nodes {
		readNotes(t, app.PinTo(node.PublicIP), notes)
	}
	tn.Deploy(t, "go", realistic.ServerApp(t, tn.F, realistic.AppGo, "go-v2"), notesApp, "--env", "APP_VERSION=go-v2", "--update")
	tn.EveryNodeServes(t, u, "/version", `"version":"go-v2"`)
	requireReplicas(t, tn, "go", notesApp)
	tn.N.CLI.MustOK(t, "app", "rollback", notesApp, "--version", "1")
	tn.EveryNodeServes(t, u, "/version", `"version":"go-v1"`)
	requireReplicas(t, tn, "go", notesApp)
	deleteAndCheckTeardown(t, tn, notesApp)
}

// writeNotes creates notesTotal notes from noteWriters clients at once.
func writeNotes(t *testing.T, f *fleet.Fleet, app *gw.Client) []note {
	t.Helper()
	var mu sync.Mutex
	var out []note
	samples := realistic.Burst(t.Context(), noteWriters, notesTotal, func(ctx context.Context, w, i int) error {
		text := fmt.Sprintf("note %d from writer %d at %s", i, w, time.Now().UTC().Format(time.RFC3339Nano))
		r, err := app.Send(ctx, gw.Req{Method: http.MethodPost, Path: "/notes", Body: []byte(text), Header: http.Header{"Content-Type": {"text/plain"}}})
		if err != nil {
			return err
		}
		var n note
		if r.Status != http.StatusCreated || json.Unmarshal(r.Body, &n) != nil || n.ID == "" || n.Cid == "" {
			return fmt.Errorf("HTTP %d %.200s", r.Status, r.Body)
		}
		n.Text = text
		mu.Lock()
		out = append(out, n)
		mu.Unlock()
		return nil
	})
	sum := realistic.Summarize("go-notes-write", samples, nil)
	realistic.WriteJSON(t, f, feature, "go-notes-write.json", sum)
	if sum.Errors > 0 {
		t.Fatalf("writing notes failed: %s (first: %s)", sum, sum.FirstError)
	}
	return out
}

// readNotes reads every note back through c, waiting out pin propagation.
func readNotes(t *testing.T, c *gw.Client, notes []note) {
	t.Helper()
	for _, n := range notes {
		eventually.Require(t, pollEvery, notePropagat, "note "+n.ID+" through "+c.PinnedIP(), func() (bool, error) {
			var got note
			if _, err := getJSON(t.Context(), c, "/notes/"+url.PathEscape(n.ID), "", &got); err != nil {
				return false, err
			}
			if got.Text != n.Text || got.Cid != n.Cid {
				return false, eventually.Stop(fmt.Errorf("note %s read back as %+v, want %q at %s", n.ID, got, n.Text, n.Cid))
			}
			return true, nil
		})
	}
}

// deleteAndCheckTeardown deletes the app and waits until no node runs it:
// the replica is torn down over /v1/internal/deployments/replica/teardown.
func deleteAndCheckTeardown(t *testing.T, tn *realistic.Tenant, name string) {
	t.Helper()
	r := tn.C.MustSend(t, gw.Req{Method: http.MethodDelete, Path: "/v1/deployments/delete?name=" + url.QueryEscape(name), Bearer: tn.Admin.Bearer})
	r.Expect(t, http.StatusOK)
	eventually.Require(t, pollEvery, teardownWait, name+" stopped on every node", func() (bool, error) {
		left := appUnitNodes(t, tn, "go", name)
		return len(left) == 0, fmt.Errorf("still active on %d node(s)", len(left))
	})
}
