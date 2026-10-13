//go:build e2e_fleet

package relayreporter

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// relayNodes are the nodes of the target whose Tor relay runs; the tests are not
// applicable on a target that installed none (the provisioned fleet installs no
// Tor role, stagenet does).
func relayNodes(t *testing.T) (*chain.Chain, []fleet.Node) {
	t.Helper()
	c := chain.New(t)
	var out []fleet.Node
	for _, n := range c.F.State.Nodes {
		if c.F.Unit(t, n, constants.GlobalTorRelayUnit) == infra.UnitActive {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		harness.SkipNotApplicable(t, "no node of this target runs a Tor relay: `orama setup` installs one on a network that pins a Tor network file (website/src/docs/operator/tor-network.mdx, Relays)")
	}
	return c, out
}

// relayInfo is the relay's entry of `orama global tor info --json` on n.
func relayInfo(t *testing.T, c *chain.Chain, n fleet.Node) tornet.NodeInfo {
	t.Helper()
	out := infra.OnNode(t, c.F, n, "global", "tor", "info", "--json")
	infra.ExpectNodeExit(t, n.Name+" tor info", out, infra.ExitOK)
	var infos []tornet.NodeInfo
	if err := json.Unmarshal([]byte(out.Stdout), &infos); err != nil {
		t.Fatalf("%s: tor info --json: %v\n%s", n.Name, err, out.Stdout)
	}
	for _, i := range infos {
		if i.Home == constants.GlobalTorRelayHome {
			if i.Fingerprint == "" || i.Ed25519ID == "" {
				t.Fatalf("%s: the relay has not written its identity yet: %+v", n.Name, i)
			}
			return i
		}
	}
	t.Fatalf("%s: tor info has no entry for %s: %+v", n.Name, constants.GlobalTorRelayHome, infos)
	return tornet.NodeInfo{}
}

// chainRelay is x/relay's record of a relay.
type chainRelay struct {
	NodeID         string `json:"node_id"`
	RSAFingerprint string `json:"rsa_fingerprint"`
	Ed25519ID      string `json:"ed25519_id"`
	Operator       string `json:"operator"`
	Exit           bool   `json:"exit"`
}

func (r chainRelay) fingerprintHex() string {
	raw, _ := base64.StdEncoding.DecodeString(r.RSAFingerprint)
	return strings.ToUpper(hex.EncodeToString(raw))
}

// After `orama setup`, each node that runs a Tor relay has that relay registered with x/relay by the
// fingerprint and ed25519 identity the relay itself made, on the node and operator that run it, and
// the node binds the relay's ed25519 key as its "relay" service key. A relay the chain does not know is
// one no reporter reports on and no epoch pays.
func TestRelay_setupRegisteredEachRelayWithItsNodeAndOperator(t *testing.T) {
	c, nodes := relayNodes(t)
	for _, n := range nodes {
		info := relayInfo(t, c, n)
		var v struct {
			Relay chainRelay `json:"relay"`
		}
		out := c.QueryOut(t, n, "relay", "relay", strings.ToLower(info.Fingerprint))
		if out.Exit != 0 {
			t.Errorf("%s: relay %s is not registered with x/relay (orama setup registers it; `orama global tor relay-identity` shows what it signs): %s", n.Name, info.Fingerprint, out.Stderr)
			continue
		}
		if err := json.Unmarshal([]byte(out.Stdout), &v); err != nil {
			t.Fatalf("%s: relay %s: %v", n.Name, info.Fingerprint, err)
		}
		if got := v.Relay.fingerprintHex(); got != info.Fingerprint {
			t.Errorf("%s: the chain holds fingerprint %s, the relay made %s", n.Name, got, info.Fingerprint)
		}
		want, err := base64.RawStdEncoding.DecodeString(info.Ed25519ID)
		if err != nil {
			t.Fatalf("%s: tor info ed25519 %q: %v", n.Name, info.Ed25519ID, err)
		}
		if got, _ := base64.StdEncoding.DecodeString(v.Relay.Ed25519ID); !bytes.Equal(got, want) {
			t.Errorf("%s: the chain holds ed25519 identity %x for the relay, the relay made %x", n.Name, got, want)
		}
		requireNodeBindsTheRelay(t, c, n, v.Relay, want)
	}
}

// requireNodeBindsTheRelay checks x/nodes' record of relay.NodeID: the operator that owns the relay owns the
// node, the node holds ROLE_RELAY, and its "relay" ed25519 binding is the relay's identity.
func requireNodeBindsTheRelay(t *testing.T, c *chain.Chain, n fleet.Node, relay chainRelay, ed25519ID []byte) {
	t.Helper()
	var v struct {
		Node struct {
			Operator string   `json:"operator"`
			Roles    []string `json:"roles"`
			Bindings []struct {
				Service string `json:"service"`
				Pubkey  string `json:"pubkey"`
			} `json:"bindings"`
		} `json:"node"`
	}
	c.Query(t, n, &v, "nodes", "node", relay.NodeID)
	if v.Node.Operator != relay.Operator {
		t.Errorf("%s: relay %s pays %s, but its node %s belongs to %s", n.Name, relay.fingerprintHex(), relay.Operator, relay.NodeID, v.Node.Operator)
	}
	var hasRole bool
	for _, role := range v.Node.Roles {
		hasRole = hasRole || role == "ROLE_RELAY"
	}
	if !hasRole {
		t.Errorf("%s: node %s holds roles %v, not ROLE_RELAY", n.Name, relay.NodeID, v.Node.Roles)
	}
	var bound bool
	for _, b := range v.Node.Bindings {
		if b.Service != clusterreg.RelayService {
			continue
		}
		if got, _ := base64.StdEncoding.DecodeString(b.Pubkey); bytes.Equal(got, ed25519ID) {
			bound = true
		}
	}
	if !bound {
		t.Errorf("%s: node %s binds no ed25519 key for service %q equal to the relay's identity %x", n.Name, relay.NodeID, clusterreg.RelayService, ed25519ID)
	}
}

// `orama global tor relay-identity` signs on the relay's node, as root, what setup sends: the printed
// fingerprint is the relay's, the cross-certificate verifies under the relay's ed25519 key, and the RSA
// proof verifies under the key the fingerprint is the SHA-1 of. A user that is not root is refused before any key is read.
func TestRelayIdentity_signsWhatTheChainChecksAndRefusesANonRoot(t *testing.T) {
	c, nodes := relayNodes(t)
	n := nodes[0]
	info := relayInfo(t, c, n)
	var v struct {
		Relay chainRelay `json:"relay"`
	}
	c.Query(t, n, &v, "relay", "relay", strings.ToLower(info.Fingerprint))
	args := []string{"global", "tor", "relay-identity", "--chain-id", c.ID, "--operator", v.Relay.Operator, "--node-id", v.Relay.NodeID}
	if v.Relay.Exit {
		args = append(args, "--exit")
	}

	infra.ExpectNodeExit(t, n.Name+" relay-identity as a user that is not root", infra.OnNodeUnprivileged(t, c.F, n, args...), infra.ExitUsage, "must be run as root")

	out := infra.OnNode(t, c.F, n, args...)
	infra.ExpectNodeExit(t, n.Name+" relay-identity", out, infra.ExitOK, info.Fingerprint)
	var doc struct {
		RSAFingerprint string `json:"rsa_fingerprint"`
		Service        string `json:"service"`
		KeyType        string `json:"key_type"`
		Pubkey         string `json:"pubkey"`
		CrossCert      string `json:"cross_cert"`
		RSAPublicKey   string `json:"rsa_public_key"`
		RSASignature   string `json:"rsa_signature"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &doc); err != nil {
		t.Fatalf("%s: relay-identity printed %q: %v", n.Name, out.Stdout, err)
	}
	if doc.Service != clusterreg.RelayService || doc.KeyType != "ed25519" || doc.RSAFingerprint != info.Fingerprint {
		t.Errorf("%s: relay-identity printed %+v", n.Name, doc)
	}
	unhex := func(name, v string) []byte {
		raw, err := hex.DecodeString(v)
		if err != nil {
			t.Fatalf("%s: %s %q: %v", n.Name, name, v, err)
		}
		return raw
	}
	pub, cert, fingerprint := unhex("pubkey", doc.Pubkey), unhex("cross_cert", doc.CrossCert), unhex("rsa_fingerprint", doc.RSAFingerprint)
	if !ed25519.Verify(pub, clusterreg.RelayCrossCertMessage(v.Relay.NodeID, fingerprint, v.Relay.Exit), cert) {
		t.Errorf("%s: the cross-certificate does not verify under the relay's key for node %s", n.Name, v.Relay.NodeID)
	}
	// The RSA proof: the public key is the one the fingerprint is the SHA-1 of, and its signature over the
	// node and the relay's ed25519 key verifies under it.
	der := unhex("rsa_public_key", doc.RSAPublicKey)
	if sum := sha1.Sum(der); !bytes.Equal(sum[:], fingerprint) { // #nosec G401 -- a Tor fingerprint is SHA-1 by definition
		t.Errorf("%s: the RSA public key does not hash to the fingerprint", n.Name)
	}
	rsaPub, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		t.Fatalf("%s: rsa_public_key: %v", n.Name, err)
	}
	proof := sha256.Sum256(clusterreg.RelayRSAProofMessage(v.Relay.NodeID, pub, v.Relay.Exit))
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, proof[:], unhex("rsa_signature", doc.RSASignature)); err != nil {
		t.Errorf("%s: the RSA proof does not verify: %v", n.Name, err)
	}
}
