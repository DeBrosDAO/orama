//go:build e2e_fleet

package invitejoin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// nodePublicKeyCmd prints the base64 of the raw 32-byte Ed25519 public half
// of the node's own key, the form node_credentials stores (migration 055).
const nodePublicKeyCmd = "openssl pkey -in " + infra.NodeKeyPath + " -pubout -outform DER | tail -c 32 | base64 -w0"

// TestNodeIdentity_enrolledPublicHalfOnly: every node signs with an Ed25519
// key it generated itself, kept 0600 on the node; the cluster holds only its
// public half in node_credentials, keyed by the node's peer id, unrevoked
// (docs/whitepaper/technical-reference/vol1/16-secrets-and-keys.md "A node signs with an Ed25519 key it generated itself").
func TestNodeIdentity_enrolledPublicHalfOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	keys := map[string]string{}
	for _, entry := range r.Nodes {
		n, err := infra.NodeByHost(f, entry.Host)
		if err != nil {
			t.Fatal(err)
		}
		infra.RequireStat(t, f, n, infra.NodeKeyPath, "orama", "orama", "600")
		pub := strings.TrimSpace(f.MustExec(t, n, nodePublicKeyCmd).Stdout)
		peer := entry.Report.RQLite.NodeID
		q := infra.IndexQuery(t, f, f.State.Nodes[0],
			"SELECT public_key, revoked_at IS NULL FROM node_credentials WHERE node_id = ?", peer)
		if len(q.Values) != 1 {
			t.Errorf("%s: %d node_credentials rows for peer %s, want 1", n.Name, len(q.Values), peer)
			continue
		}
		if got, _ := q.Values[0][0].(string); got != pub {
			t.Errorf("%s: the cluster holds public key %q, the node's key is %q", n.Name, got, pub)
		}
		if live, _ := q.Values[0][1].(float64); live != 1 {
			t.Errorf("%s: its credential is revoked", n.Name)
		}
		if other, dup := keys[pub]; dup {
			t.Errorf("%s and %s share a node key", n.Name, other)
		}
		keys[pub] = n.Name
	}
	q := infra.IndexQuery(t, f, f.State.Nodes[0], "SELECT COUNT(*) FROM node_credentials WHERE public_key LIKE '%PRIVATE%'")
	if c, _ := q.Values[0][0].(float64); c != 0 {
		t.Error("node_credentials holds private key material")
	}
}

// TestJoin_signerMismatchRefusedTokenKept: a joiner that expects other
// archive signers than the cluster's is refused with 409 naming the
// cluster's signers, and the invite is not used (docs/whitepaper/technical-reference/vol1/29-build-signing-and-release.md "Supply
// Chain": the minting node refuses a mismatch with 409 before the invite is
// spent or a peer row written).
func TestJoin_signerMismatchRefusedTokenKept(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	infra.ForgetPhantomOnCleanup(t, unusedPublicIP)
	n := f.State.Nodes[0]
	inv := infra.DecodeInvite(t, mint(t).Invite)
	stranger, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"token": inv.Token, "wg_public_key": infra.NewWGKey(t),
		"public_ip": unusedPublicIP, "expected_archive_signers": []string{stranger.Address()}})
	if err != nil {
		t.Fatal(err)
	}
	r := infra.PostJoin(t, n, raw)
	expectRefusal(t, r.Status, r.Body, http.StatusConflict, "the invite was not used")
	if !strings.Contains(string(r.Body), strings.ToLower(f.State.OperatorAddress)) {
		t.Errorf("the refusal does not name the cluster's signer: %.300q", r.Body)
	}
	bad, err := json.Marshal(map[string]any{"token": inv.Token, "wg_public_key": infra.NewWGKey(t),
		"public_ip": unusedPublicIP, "expected_archive_signers": []string{"not-an-address"}})
	if err != nil {
		t.Fatal(err)
	}
	r = infra.PostJoin(t, n, bad)
	expectRefusal(t, r.Status, r.Body, http.StatusBadRequest, "not a list of addresses")
	if row := infra.ReadInvite(t, f, n, inv.Token); !row.Found || row.Used {
		t.Fatalf("a refused join used the invite: %+v", row)
	}
	peers := infra.IndexQuery(t, f, n, "SELECT COUNT(*) FROM wireguard_peers WHERE public_ip = ?", unusedPublicIP)
	if c, _ := peers.Values[0][0].(float64); c != 0 {
		t.Fatal("a refused join wrote a wireguard_peers row")
	}
}
