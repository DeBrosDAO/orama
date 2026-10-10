//go:build e2e_fleet

package namespaces

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Creation codes (core/pkg/gateway/handlers/namespace/create_handler.go).
const (
	codeTaken   = "NAMESPACE_TAKEN"
	codeName    = "NAMESPACE_NAME_INVALID"
	statusProv  = "provisioning"
	racingWalls = 6
)

// create posts a creation as owner and decodes the answer.
func create(t testing.TB, owner *gw.User, name string) (tenancy.Created, *gw.Response) {
	t.Helper()
	resp := tenancy.Create(t, owner, name)
	var c tenancy.Created
	if resp.Status < 300 {
		if err := resp.Decode(&c); err != nil {
			t.Fatal(err)
		}
	}
	return c, resp
}

// adoptIfCreated takes a namespace that a create the test expects to be
// refused created anyway, so a regression fails the test without leaking the
// namespace: Adopt registers its deletion.
func adoptIfCreated(t testing.TB, f *fleet.Fleet, owner *gw.User, c tenancy.Created, resp *gw.Response) {
	t.Helper()
	if resp.Status >= http.StatusOK && resp.Status < http.StatusMultipleChoices {
		tenancy.Adopt(t, f, owner, c)
	}
}

// TestNamespaceCreate_apiProvisionsAndServes: POST /v1/namespaces answers 202
// with the cluster being provisioned and a poll URL (docs/whitepaper/technical-reference/appendices/i-api-surface.md
// "Namespace management"); the status route reports progress, and "ready"
// means a real request through https://ns-<name> succeeds.
func TestNamespaceCreate_apiProvisionsAndServes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	owner := tenancy.Creator(t, f)
	name := ns.UniqueName(t.Name())
	c, resp := create(t, owner, name)
	resp.Expect(t, http.StatusAccepted)
	if c.Name != name || c.Status != statusProv || c.ClusterID == "" || c.PollURL == "" || !strings.EqualFold(c.Owner, owner.Wallet.Address()) {
		t.Fatalf("creation answered %+v", c)
	}
	n := tenancy.Adopt(t, f, owner, c)
	st, err := tenancy.Status(t.Context(), owner.Client, c.ClusterID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.RQLiteReady || !st.OlricReady || !st.GatewayReady || !st.DNSReady || st.Namespace != name {
		t.Errorf("ready namespace reports %+v", st)
	}
	tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
	var listed struct {
		Namespaces []struct {
			Name          string `json:"name"`
			ClusterStatus string `json:"cluster_status"`
		} `json:"namespaces"`
	}
	lobby := tenancy.Cred{Bearer: owner.Token()}
	if err := tenancy.Get(t, owner.Client, tenancy.PathList, lobby).Expect(t, http.StatusOK).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Namespaces) != 1 || listed.Namespaces[0].Name != name || listed.Namespaces[0].ClusterStatus != ns.StatusReady {
		t.Fatalf("the owner's list is %+v, want only %s ready", listed.Namespaces, name)
	}
}

// TestNamespaceCreate_cliCreatesAndLists: `orama namespace create` makes the
// operator the owner and `orama namespace list` shows it ready
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama namespace create").
func TestNamespaceCreate_cliCreatesAndLists(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	var rows []struct{ Name, Cluster, Active string }
	if err := oramacli.DecodeJSON(n.CLI.MustOK(t, "namespace", "list", "--json"), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == n.Name && r.Cluster == ns.StatusReady && r.Active == "yes" {
			return
		}
	}
	t.Fatalf("%s is not listed ready and active: %+v", n.Name, rows)
}

func TestNamespaceCreate_cliRefusals(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	for _, args := range [][]string{{"namespace", "create"}, {"namespace", "create", "default"}, {"namespace", "create", "-bad-"},
		{"namespace", "create", strings.Repeat("x", 41)}, {"namespace", "no-such-subcommand"}} {
		res, err := cli.Run(t.Context(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 {
			t.Errorf("orama %s succeeded: %s", strings.Join(args, " "), res.Stdout)
		}
	}
	out := cli.MustOK(t, "namespace", "--help").Stdout
	for _, sub := range []string{"backup", "backup-open", "backup-seal", "create", "delete", "disable", "enable", "keys", "list", "repair", "restore", "restore-key", "rqlite", "webrtc-status"} {
		if !strings.Contains(out, sub) {
			t.Errorf("orama namespace --help does not list %s", sub)
		}
	}
}

// TestNamespaceCreate_takenNameRefused: a name that exists is NAMESPACE_TAKEN
// for anyone, the owner included, and the first namespace is untouched.
func TestNamespaceCreate_takenNameRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	ownerInLobby := &gw.User{Wallet: n.Owner.Wallet, Client: n.Owner.Client}
	s, err := n.Owner.Client.For(t).SignIn(t.Context(), n.Owner.Wallet, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerInLobby.Session = s
	// This session only: logging the wallet out everywhere would end the
	// namespace session the teardown deletes with.
	t.Cleanup(func() { endSession(t, ownerInLobby) })
	for who, u := range map[string]*gw.User{"another wallet": tenancy.Creator(t, f), "the owner": ownerInLobby} {
		c, resp := create(t, u, n.Name)
		adoptIfCreated(t, f, u, c, resp)
		if resp.Status != http.StatusConflict || resp.ErrorCode() != codeTaken {
			t.Errorf("%s re-creating %s: want 409 %s, got %d %s", who, n.Name, codeTaken, resp.Status, resp.Body)
		}
	}
	tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
}

// TestNamespaceCreate_needsASignedInWallet: a namespace's owner is a wallet,
// so no credential, a garbage one and an API key are all refused
// (create_handler.go walletFromContext).
func TestNamespaceCreate_needsASignedInWallet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	c := harness.GW(t)
	body := map[string]string{"name": ns.UniqueName(t.Name())}
	for who, cred := range map[string]tenancy.Cred{"nobody": {}, "garbage": {Bearer: "a.b.c"}, "api key": {APIKey: tenancy.APIKey(t, n, "admin")}} {
		if r := tenancy.Post(t, c, tenancy.PathCreate, cred, body); r.Status < 400 || r.Status >= 500 {
			t.Errorf("%s created a namespace: %d %s", who, r.Status, r.Body)
		}
	}
	owner := tenancy.Creator(t, f)
	for name, raw := range map[string][]byte{"not json": []byte("name=x"), "wrong type": []byte(`{"name":5}`)} {
		if r := tenancy.Post(t, c, tenancy.PathCreate, tenancy.Cred{Bearer: owner.Token()}, raw); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, r.Status)
		}
	}
	tenancy.Get(t, c, tenancy.PathCreate, tenancy.Cred{Bearer: owner.Token()}).Expect(t, http.StatusMethodNotAllowed)
}

// TestNamespaceCreate_concurrentSameNameOneWinner: wallets race to create the
// same name; exactly one wins and the rest are NAMESPACE_TAKEN.
func TestNamespaceCreate_concurrentSameNameOneWinner(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	name := ns.UniqueName(t.Name())
	owners := make([]*gw.User, racingWalls)
	for i := range owners {
		owners[i] = tenancy.Creator(t, f)
	}
	results := make([]*gw.Response, racingWalls)
	var wg sync.WaitGroup
	for i, u := range owners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = u.Client.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: tenancy.PathCreate, Bearer: u.Token(),
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(fmt.Sprintf(`{"name":%q}`, name))})
		}()
	}
	wg.Wait()
	winners := 0
	for i, r := range results {
		switch {
		case r == nil:
			t.Errorf("racer %d got no answer", i)
		case r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices:
			winners++
			var c tenancy.Created
			if err := r.Decode(&c); err != nil {
				t.Fatal(err)
			}
			tenancy.Adopt(t, f, owners[i], c)
			if r.Status != http.StatusAccepted {
				t.Errorf("racer %d created %s with %d, want 202", i, name, r.Status)
			}
		case r.Status != http.StatusConflict:
			t.Errorf("racer %d: want 202 or 409, got %d %s", i, r.Status, r.Body)
		}
	}
	if winners != 1 {
		t.Fatalf("%d racers created %s, want exactly one", winners, name)
	}
}
