//go:build e2e_fleet

package dnstls

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// servedLeaf is the SHA-256 of the leaf certificate node ip serves for sni.
func servedLeaf(t *testing.T, ip, sni string) string {
	t.Helper()
	st, err := dialTLS(t, ip, sni, nil)
	if err != nil {
		t.Fatalf("%s: TLS for %s: %v", ip, sni, err)
	}
	sum := sha256.Sum256(st.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}

// TestTLS_everyNodeServesTheClustersOneCertificate: the base name and a name
// under it are served with the same certificate by every node — one obtained
// for the cluster and shared through its store, not one per node, which would
// spend Let's Encrypt's five-a-week limit on one name set with every install
// (website/src/docs/contributor/architecture-reference.mdx "TLS/HTTPS": one certificate per cluster).
func TestTLS_everyNodeServesTheClustersOneCertificate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	for _, sni := range []string{base, under(t, base)} {
		seen := map[string][]string{}
		for _, n := range f.State.Nodes {
			leaf := servedLeaf(t, n.PublicIP, sni)
			seen[leaf] = append(seen[leaf], n.Name)
		}
		if len(seen) != 1 {
			t.Errorf("%s is served with %d different certificates across the nodes: %v", sni, len(seen), seen)
		}
	}
}

// TestTLSStore_refusesCallsNotFromCaddy: a process on the node with no stamp,
// a stamp under a key no node holds, or a replay of a valid call gets 404 —
// the route does not confirm it exists (docs/whitepaper/technical-reference/vol1/25-tls-and-certificates.md "Certificates").
func TestTLSStore_refusesCallsNotFromCaddy(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	load := map[string]any{"op": "load", "key": "acme"}
	for _, key := range []string{edge.KeyNone, edge.KeyRandom} {
		for _, p := range (edge.TLSStoreCall{Body: load, Key: key}).Run(t, f, n) {
			if p.Status != http.StatusNotFound {
				t.Errorf("%s: key %s: %d, want 404: %.200s", n.Name, key, p.Status, p.Body)
			}
		}
	}
	ps := (edge.TLSStoreCall{Body: load, Key: edge.KeyReal, Replay: true}).Run(t, f, n)
	if len(ps) != 2 || ps[0].Status != http.StatusOK || ps[1].Status != http.StatusNotFound {
		t.Errorf("%s: a signed call and its replay: %+v, want 200 then 404", n.Name, ps)
	}
}

// TestTLSStore_aStampIsGoodForOneProcessOnly: a v3 stamp made for the port the
// call is sent to is accepted (the v2 stamp beside it is not needed), and one
// made for another process's port on the same node is 404, so a stamp captured
// on its way to one gateway process cannot be replayed to a sibling on the
// node (docs/whitepaper/technical-reference/vol1/15-inter-node-trust.md "Coordination MAC v3").
func TestTLSStore_aStampIsGoodForOneProcessOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	load := map[string]any{"op": "load", "key": "acme"}
	ps := (edge.TLSStoreCall{Body: load, Key: edge.KeyReal, Stamps: "v3"}).Run(t, f, n)
	if len(ps) != 1 || ps[0].Status != http.StatusOK {
		t.Errorf("%s: a v3 stamp for the gateway's own port: %+v, want 200", n.Name, ps)
	}
	ps = (edge.TLSStoreCall{Body: load, Key: edge.KeyReal, Stamps: "v3-other-port"}).Run(t, f, n)
	if len(ps) != 1 || ps[0].Status != http.StatusNotFound {
		t.Errorf("%s: a v3 stamp made for another port: %+v, want 404", n.Name, ps)
	}
}

// TestTLSStore_holdsTheWildcardSealed: the store every node's Caddy uses holds
// the cluster's *.<base> certificate and key, sealed — what the gateway keeps
// in the registry is never a key in the clear (docs/whitepaper/technical-reference/vol1/25-tls-and-certificates.md
// "Certificates").
func TestTLSStore_holdsTheWildcardSealed(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	name := "wildcard_." + f.State.BaseDomain
	ps := (edge.TLSStoreCall{Body: map[string]any{"op": "list", "key": "certificates", "recursive": true}, Key: edge.KeyReal}).Run(t, f, n)
	if len(ps) != 1 || ps[0].Status != http.StatusOK {
		t.Fatalf("%s: list: %+v", n.Name, ps)
	}
	var list struct{ Keys []string }
	if err := json.Unmarshal([]byte(ps[0].Body), &list); err != nil {
		t.Fatalf("%s: list answer %q: %v", n.Name, ps[0].Body, err)
	}
	var keyPath string
	for _, k := range list.Keys {
		if strings.HasSuffix(k, "/"+name+"/"+name+".key") {
			keyPath = k
		}
	}
	if keyPath == "" {
		t.Fatalf("%s: the store holds no %s key among %v", n.Name, name, list.Keys)
	}
	ps = (edge.TLSStoreCall{Body: map[string]any{"op": "load", "key": keyPath}, Key: edge.KeyReal}).Run(t, f, n)
	var got struct {
		Exists *bool
		Value  string
	}
	if len(ps) != 1 || json.Unmarshal([]byte(ps[0].Body), &got) != nil || got.Exists == nil || !*got.Exists {
		t.Fatalf("%s: load %s: %+v", n.Name, keyPath, ps)
	}
	if !strings.HasPrefix(got.Value, "v1.") || strings.Contains(got.Value, "PRIVATE KEY") {
		t.Errorf("%s: the stored key is not sealed: %.40s…", n.Name, got.Value)
	}
}

// TestTLSStore_exportedWildcardIsTheServedOne: the *.<base> pair the cluster
// gateway exports for TURN on every node is the certificate the nodes serve
// for a name under the base (website/src/docs/developer/webrtc.mdx: TURNS serves the cluster's
// wildcard).
func TestTLSStore_exportedWildcardIsTheServedOne(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	served := servedLeaf(t, f.State.Nodes[0].PublicIP, under(t, f.State.BaseDomain))
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, "cat "+edge.WildcardCertPath)
		block, _ := pem.Decode([]byte(out.Stdout))
		if block == nil {
			t.Errorf("%s: %s holds no PEM certificate", n.Name, edge.WildcardCertPath)
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			t.Errorf("%s: %s: %v", n.Name, edge.WildcardCertPath, err)
			continue
		}
		sum := sha256.Sum256(block.Bytes)
		if got := hex.EncodeToString(sum[:]); got != served {
			t.Errorf("%s: the exported wildcard %s… is not the served one %s…", n.Name, got[:16], served[:16])
		}
	}
}
