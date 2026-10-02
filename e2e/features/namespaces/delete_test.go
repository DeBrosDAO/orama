//go:build e2e_fleet

package namespaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// dnsTeardownBudget bounds the removal of the namespace's DNS records from the
// registry after the delete.
const dnsTeardownBudget = ns.TeardownBudget

// deprovisioningPollEvery is how often the client-leaving test looks for the
// cluster's deprovisioning state: a teardown lasts seconds, so a slower poll
// could miss the window it has to act in.
const deprovisioningPollEvery = 200 * time.Millisecond

// exitAborted is the CLI's exit code for a declined confirmation
// (core/cmd/orama/internal/clierr CodeAborted; e2e/README.md exit codes).
const exitAborted = 7

// adopted creates a namespace through the API, as a fresh creator, and waits
// until it serves. The caller reserves room for it (tenancy.Reserve).
func adopted(t testing.TB, f *fleet.Fleet) *ns.Namespace {
	t.Helper()
	owner := tenancy.Creator(t, f)
	c, resp := create(t, owner, ns.UniqueName(t.Name()))
	resp.Expect(t, http.StatusAccepted)
	return tenancy.Adopt(t, f, owner, c)
}

func deleteNow(t testing.TB, n *ns.Namespace) {
	t.Helper()
	tenancy.Send(t, n.Client, http.MethodDelete, tenancy.PathDelete, tenancy.Owner(n), nil).Expect(t, http.StatusOK)
	eventually.Require(t, pollEvery, ns.TeardownBudget, n.Name+" to be gone", func() (bool, error) {
		return tenancy.Gone(t.Context(), n)
	})
}

// TestNamespaceDelete_tearsEverythingDown: deleting a namespace stops its
// units, removes its directories and unit environment, frees its ports,
// withdraws its DNS name and revokes its keys, on every node; the name is then
// free again and a namespace created with it starts empty.
func TestNamespaceDelete_tearsEverythingDown(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 2) // this namespace, and the one recreated with its name
	n := adopted(t, f)
	key := tenancy.APIKeyDroppedWithNamespace(t, n, "cache")
	tenancy.Post(t, n.Client, "/v1/cache/put", tenancy.Owner(n), map[string]any{"dmap": "m", "key": "k", "value": "old"}).Expect(t, http.StatusOK)
	blocks := map[string][]int{}
	for _, node := range tenancy.Members(t, f, n.Name) {
		blocks[node.Name] = tenancy.PortBlock(t, f, node, n.Name)
	}
	host := tenancy.NamespaceHost(f, n.Name)
	for _, nsNode := range tenancy.Nameservers(f) {
		if addrs, err := tenancy.ResolveAt(t.Context(), nsNode.PublicIP, host); err != nil || len(addrs) == 0 {
			t.Fatalf("%s does not resolve %s before the delete: %v %v", nsNode.Name, host, addrs, err)
		}
	}
	deleteNow(t, n)
	for _, node := range f.State.Nodes {
		eventually.Require(t, pollEvery, ns.TeardownBudget, node.Name+" to hold nothing of "+n.Name, residue(t, f, node, n.Name, blocks[node.Name]))
	}
	// The name withdrawn is the namespace's own records. Whether it still
	// resolves proves nothing where the zone has a *.<base> wildcard (stagenet
	// does): every name under the base answers then, a deleted namespace's too.
	eventually.Require(t, pollEvery, dnsTeardownBudget, "the registry to hold no DNS record of "+n.Name, func() (bool, error) {
		res := infra.IndexQuery(t, f, f.State.Nodes[0],
			`SELECT fqdn FROM dns_records WHERE namespace = ? OR fqdn LIKE ?`,
			"namespace:"+n.Name, "%.ns-"+n.Name+"."+f.State.BaseDomain+".")
		if res.Error != "" {
			return false, fmt.Errorf("query: %s", res.Error)
		}
		if len(res.Values) != 0 {
			return false, fmt.Errorf("still holds %v", res.Values)
		}
		return true, nil
	})
	if _, resp, err := harness.GW(t).Token(t.Context(), key); err == nil || resp == nil || resp.Status != http.StatusUnauthorized {
		t.Errorf("the deleted namespace's key still exchanges for a token: %v", err)
	}
	reborn := recreate(t, f, n)
	tenancy.Post(t, reborn.Client, "/v1/cache/get", tenancy.Owner(reborn), map[string]any{"dmap": "m", "key": "k"}).Expect(t, http.StatusNotFound)
}

// residue is an eventually probe for "node holds no unit (running or enabled), directory,
// environment or listener of name".
func residue(t testing.TB, f *fleet.Fleet, node fleet.Node, name string, block []int) func() (bool, error) {
	return func() (bool, error) {
		for _, unit := range tenancy.TenantUnits(name) {
			if s := f.Unit(t, node, unit); s == "active" || s == "activating" {
				return false, fmt.Errorf("%s is %s", unit, s)
			}
			// Stopped is not gone: an enabled unit is started again by the next
			// `orama node upgrade`, which enables and restarts every namespace
			// unit it finds.
			if s := strings.TrimSpace(f.Exec(t, node, "systemctl is-enabled "+unit).Stdout); s == "enabled" {
				return false, fmt.Errorf("%s is still enabled", unit)
			}
		}
		for _, dir := range []string{tenancy.NamespacesDir + "/" + name, tenancy.UnitEnvDir + "/" + name} {
			if f.Exec(t, node, "test -e "+dir).Exit == 0 {
				return false, fmt.Errorf("%s still exists", dir)
			}
		}
		for _, l := range f.Listeners(t, node) {
			for _, p := range block {
				if l.Port == p {
					return false, fmt.Errorf("port %d is still listened on by %q", p, l.Process)
				}
			}
		}
		return true, nil
	}
}

// recreate creates a namespace with n's name as a new wallet.
func recreate(t testing.TB, f *fleet.Fleet, n *ns.Namespace) *ns.Namespace {
	t.Helper()
	owner := tenancy.Creator(t, f)
	c, resp := create(t, owner, n.Name)
	resp.Expect(t, http.StatusAccepted)
	return tenancy.Adopt(t, f, owner, c)
}

// TestNamespaceDelete_whileInUse: deleting a namespace with an open
// subscription and a writer running completes, and the socket is closed.
func TestNamespaceDelete_whileInUse(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	n := adopted(t, f)
	conn, resp, err := n.Client.DialWS(t.Context(), "/v1/pubsub/ws?"+url.Values{"topic": {"live"}}.Encode(), n.Owner.Token(), nil)
	if err != nil {
		t.Fatalf("subscribe failed (%v): %v", resp, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	closed := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				closed <- err
				return
			}
		}
	}()
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = n.Client.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: "/v1/cache/put", Bearer: n.Owner.Token(),
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"dmap":"m","key":"k` + strconv.Itoa(i) + `","value":1}`)})
		}
	}()
	deleteNow(t, n)
	close(stop)
	<-writerDone
	eventually.Require(t, pollEvery, ns.TeardownBudget, "the open socket to be closed", func() (bool, error) {
		select {
		case <-closed:
			return true, nil
		default:
			return false, fmt.Errorf("socket still open")
		}
	})
}

// TestNamespaceDelete_refusals: only a caller holding the namespace's control
// plane deletes it; the lobby cannot delete "default"; wrong methods are 405.
func TestNamespaceDelete_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	for who, cred := range map[string]tenancy.Cred{
		"runtime member": {Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()},
		"reader member":  {Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()},
		"runtime key":    {APIKey: tenancy.APIKey(t, n, "app-runtime")},
	} {
		if r := tenancy.Send(t, n.Client, http.MethodDelete, tenancy.PathDelete, cred, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d", who, r.Status)
		}
	}
	tenancy.ExpectRefused(t, tenancy.Send(t, n.Client, http.MethodDelete, tenancy.PathDelete, tenancy.Cred{}, nil), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.Get(t, n.Client, tenancy.PathDelete, tenancy.Owner(n)).Expect(t, http.StatusMethodNotAllowed)
	lobby := gw.NewUser(t, f, gw.LobbyNamespace)
	if r := tenancy.Send(t, harness.GW(t), http.MethodDelete, tenancy.PathDelete, tenancy.Cred{Bearer: lobby.Token()}, nil); r.Status < 400 || r.Status >= 500 {
		t.Errorf("a lobby session deleting: want a 4xx, got %d", r.Status)
	}
	tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
}

// TestNamespaceDelete_cliNeedsConfirmation: `orama namespace delete` without
// --force and with nothing typed deletes nothing and exits with the aborted
// code (namespace_commands.go confirmExact: clierr.Aborted). The delete
// handler deprovisions before it answers (delete_handler.go), so a delete
// that went through would already be missing from the operator's list.
func TestNamespaceDelete_cliNeedsConfirmation(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	res, err := n.CLI.Run(t.Context(), "namespace", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != exitAborted || strings.Contains(res.Stdout, "Deleting namespace") {
		t.Errorf("an unconfirmed delete: exit %d, want %d (aborted) without deleting:\n%s%s", res.Exit, exitAborted, res.Stdout, res.Stderr)
	}
	var rows []struct{ Name, Cluster string }
	if err := oramacli.DecodeJSON(n.CLI.MustOK(t, "namespace", "list", "--json"), &rows); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rows, func(r struct{ Name, Cluster string }) bool { return r.Name == n.Name && r.Cluster == ns.StatusReady }) {
		t.Fatalf("after an unconfirmed delete %s is not listed ready: %+v", n.Name, rows)
	}
	tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
}

// TestNamespaceDelete_survivesTheClientLeaving: a client that disconnects while
// the delete runs (a killed CLI, a proxy timeout) does not stop it: the
// teardown runs on a context of the removal's own, so the namespace is gone
// afterwards on every node, and a second delete sent while the first still runs
// is refused 409 NAMESPACE_DELETE_IN_PROGRESS, retryable (docs/ARCHITECTURE.md,
// namespace delete). The cluster is 'deprovisioning' from the moment the
// teardown starts, which is what the test waits on before it cuts the client.
func TestNamespaceDelete_survivesTheClientLeaving(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	n := adopted(t, f)

	sendCtx, leave := context.WithCancel(t.Context())
	defer leave()
	left := make(chan error, 1)
	go func() {
		_, err := n.Client.Send(sendCtx, gw.Req{Method: http.MethodDelete, Path: tenancy.PathDelete, Bearer: n.Owner.Token()})
		left <- err
	}()

	eventually.Require(t, deprovisioningPollEvery, ns.TeardownBudget, n.Name+" to begin deprovisioning", func() (bool, error) {
		st, err := tenancy.Status(t.Context(), n.Owner.Client, n.ClusterID)
		if err != nil {
			return false, fmt.Errorf("the delete finished before it could be interrupted: %w", err)
		}
		return st.Status == "deprovisioning", nil
	})

	// A client that retries while the first delete runs.
	second := tenancy.Send(t, n.Client, http.MethodDelete, tenancy.PathDelete, tenancy.Owner(n), nil)
	if second.Status != http.StatusConflict {
		t.Fatalf("a second delete while the first runs: want 409, got %d: %.300s", second.Status, second.Body)
	}
	var refusal struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(second.Body, &refusal); err != nil || refusal.Code != "NAMESPACE_DELETE_IN_PROGRESS" || !refusal.Retryable {
		t.Fatalf("the refusal is not a retryable NAMESPACE_DELETE_IN_PROGRESS: %.300s", second.Body)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Error("the 409 carries no Retry-After")
	}

	leave()
	if err := <-left; err == nil {
		t.Log("the first delete answered before the client left; the disconnect was not exercised")
	}
	eventually.Require(t, pollEvery, ns.TeardownBudget, n.Name+" to be gone after its client left", func() (bool, error) {
		return tenancy.Gone(t.Context(), n)
	})
}
