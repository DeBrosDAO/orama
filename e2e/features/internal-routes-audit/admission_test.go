//go:build e2e_fleet

package internalroutesaudit

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

// The node's own routes (core/pkg/nodeapi).
const (
	pathEnrolKey = "/v1/internal/node/enrol-key"
	pathRegister = "/v1/internal/node/register"
)

// stamped is a node-api request signed by signer as nodeID, rendered as the
// curl that sends it from the node's own loopback.
func stamped(t *testing.T, signer auth.NodeStampSigner, nodeID, path string, payload any) edge.NodeCurl {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	url := edge.LocalGateway(path)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SignNodeAPI(signer, req, nodeID, body, time.Now()); err != nil {
		t.Fatal(err)
	}
	return edge.NodeCurl{Method: http.MethodPost, URL: url, Body: string(body),
		Headers: append(curlHeaders(req.Header), "Content-Type: application/json")}
}

// TestNodeRegister_aNeverAdmittedIdentityIsRefused: a caller on a node's host
// that makes up a libp2p identity can enrol a key for it — that proves only that
// it holds the key, and records nothing the cluster routes on — but its
// registration is refused (403) and no dns_nodes row appears: no join admitted
// the node (docs/whitepaper/technical-reference/vol1/04-the-node-as-a-supervisor.md "A node recording itself").
func TestNodeRegister_aNeverAdmittedIdentityIsRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]

	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	id := pid.String()
	key, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { forgetCredential(t, f, n, id) })

	enrol := stamped(t, auth.NodeIdentitySigner(identity), id, pathEnrolKey, map[string]string{"public_key": key.PublicKey()}).Run(t, f, n)
	if enrol.Status != http.StatusOK {
		t.Fatalf("%s: the made-up identity's enrolment answered HTTP %d (curl exit %d): %.200s", n.Name, enrol.Status, enrol.Exit, enrol.Body)
	}
	reg := stamped(t, key, id, pathRegister, map[string]string{
		"ip_address": "203.0.113.250", "internal_ip": "10.0.0.250", "region": "local", "ssh_user": "orama",
	}).Run(t, f, n)
	if reg.Status != http.StatusForbidden {
		t.Fatalf("%s: a never-admitted node's registration answered HTTP %d (curl exit %d), want 403: %.200s",
			n.Name, reg.Status, reg.Exit, reg.Body)
	}
	q := infra.IndexQuery(t, f, n, "SELECT COUNT(*) FROM dns_nodes WHERE id = ?", id)
	if len(q.Values) != 1 || q.Values[0][0] != float64(0) {
		t.Fatalf("a refused registration left a dns_nodes row: %v", q.Values)
	}
}

// forgetCredential removes what the test may have left for its made-up identity.
func forgetCredential(t *testing.T, f *fleet.Fleet, n fleet.Node, nodeID string) {
	t.Helper()
	// dns_nodes too: if a regression let the registration through, the row
	// it wrote must not outlive the test.
	stmt, err := json.Marshal([][]any{
		{"DELETE FROM node_credentials WHERE node_id = ?", nodeID},
		{"DELETE FROM dns_nodes WHERE id = ?", nodeID},
	})
	if err != nil {
		t.Error(err)
		return
	}
	ctx, cancel := fleet.CleanupContext(t)
	defer cancel()
	cmd := infra.IndexCurl("-sS --max-time 20 -X POST -H 'Content-Type: application/json' --data-binary "+fleet.ShellQuote(string(stmt)), "/db/execute")
	if out, err := f.SSHFor(t, n).Run(ctx, cmd); err != nil || out.Exit != 0 {
		t.Errorf("%s: could not remove the test's enrolled key for %s: %v (exit %d)", n.Name, nodeID, err, out.Exit)
	}
}
