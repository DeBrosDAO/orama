//go:build e2e_fleet

package chainglobal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// docCase is one chain command and the message it must build.
type docCase struct {
	args    []string
	typeURL string
}

// proofFile writes a one-proof JSON file for `orama storage prove`.
func proofFile(t *testing.T) string {
	t.Helper()
	leaf := hex.EncodeToString(make([]byte, 1024))
	path := filepath.Join(t.TempDir(), "proofs.json")
	body := fmt.Sprintf(`[{"deal_id": 1, "slot": 0, "leaf_index": 0, "leaf": %q, "siblings": []}]`, leaf)
	if err := os.WriteFile(path, []byte(body), secretFileMode); err != nil {
		t.Fatal(err)
	}
	return path
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func docCases(t *testing.T, c *chain.Chain, s signer, bindingFile string) map[string]docCase {
	op, other := s.k.Address, c.Validator(t, c.Node(t, 1)).Address
	node, cluster := chain.UniqueID(t, "e2e-doc-"), chain.UniqueID(t, "e2e-doc-")
	return map[string]docCase{
		"cluster register-onchain": {[]string{"cluster", "register-onchain", "--operator", op, "--id", cluster,
			"--base-domain", c.F.State.BaseDomain, "--endpoint", c.F.State.GatewayURL}, "/orama.nodes.v1.MsgRegisterCluster"},
		"cluster retire-onchain": {[]string{"cluster", "retire-onchain", "--operator", op, "--id", cluster}, "/orama.nodes.v1.MsgRetireCluster"},
		"global register": {[]string{"global", "register", "--operator", op, "--id", node, "--role", "relay", "--hot-key", other,
			"--binding", bindingFile}, "/orama.nodes.v1.MsgRegisterNode"},
		"global bond":     {[]string{"global", "bond", "--operator", op, "--id", node, "--role", "storage", "--amount", "1"}, "/orama.nodes.v1.MsgBondNode"},
		"global unbond":   {[]string{"global", "unbond", "--operator", op, "--id", node, "--role", "storage", "--amount", "1"}, "/orama.nodes.v1.MsgUnbondNode"},
		"global capacity": {[]string{"global", "capacity", "--operator", op, "--id", node, "--bytes", "1024"}, "/orama.nodes.v1.MsgDeclareCapacity"},
		"global retire":   {[]string{"global", "retire", "--operator", op, "--id", node}, "/orama.nodes.v1.MsgRetireNode"},
		"storage grant": {[]string{"storage", "grant", "--signer", op, "--grantee", other, "--spend-limit", "1000",
			"--max-piece-bytes", "2048", "--max-duration-epochs", "5"}, "/orama.storage.v1.MsgGrantDealAuthorization"},
		"storage revoke": {[]string{"storage", "revoke", "--signer", op, "--grantee", other}, "/orama.storage.v1.MsgRevokeDealAuthorization"},
		"storage create": {[]string{"storage", "create", "--signer", op, "--class", "public-pin", "--nonce", randomHex(t, 32),
			"--piece", randomHex(t, 32) + ":1024", "--price", "1", "--duration-epochs", "2"}, "/orama.storage.v1.MsgCreateDeal"},
		"storage extend":  {[]string{"storage", "extend", "--signer", op, "--deal-id", "1", "--extra-epochs", "1"}, "/orama.storage.v1.MsgExtendDeal"},
		"storage accept":  {[]string{"storage", "accept", "--signer", op, "--id", node, "--deal-id", "1", "--slot", "0"}, "/orama.storage.v1.MsgAcceptDeal"},
		"storage decline": {[]string{"storage", "decline", "--signer", op, "--id", node, "--deal-id", "1", "--reason", "e2e"}, "/orama.storage.v1.MsgDeclineDeal"},
		"storage prove":   {[]string{"storage", "prove", "--signer", op, "--id", node, "--file", proofFile(t)}, "/orama.storage.v1.MsgSubmitProofs"},
	}
}

// edBinding binds a fresh ed25519 relay key for operator through the CLI
// and returns the binding file.
func edBinding(t *testing.T, c *chain.Chain, operator string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, file := bind(t, c.ID, operator, "relay", writeSecret(t, "relay.key", priv.Seed()), "ed25519")
	return file
}

// TestOnchainDocs_withoutNodeOnlyPrintTheSignDocument: every chain command of
// the CLI (orama cluster register-onchain|retire-onchain, orama global
// register|bond|unbond|capacity|retire, orama storage
// grant|revoke|create|extend|accept|decline|prove) without --node prints a
// SIGN_MODE_DIRECT sign document for the given chain id, account number,
// sequence, fee and gas, carrying exactly one message of the documented type
// whose signer field is the given account, and submits nothing
// (docs/CLI_REFERENCE.md: "Without --node it prints the sign document").
func TestOnchainDocs_withoutNodeOnlyPrintTheSignDocument(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, chain.OperatorNode)
	cases := docCases(t, c, s, edBinding(t, c, s.k.Address))
	for name, tc := range cases {
		d := doc(t, c, s, tc.args...)
		requireDoc(t, name, d, c.ID, s.account, s.sequence, tc.typeURL)
		if got := d.msg.Str(1); got != s.k.Address {
			t.Errorf("%s: message signer field %q, want %s", name, got, s.k.Address)
		}
	}
	cluster, node := cases["cluster register-onchain"].args[4], cases["global register"].args[4]
	if out := c.QueryFails(t, s.k.Node, "nodes", "cluster", cluster); !chain.NotFound(out) {
		t.Errorf("cluster %s exists although the command only printed its document: %s", cluster, out)
	}
	if out := c.QueryFails(t, s.k.Node, "nodes", "node", node); !chain.NotFound(out) {
		t.Errorf("node %s exists although the command only printed its document: %s", node, out)
	}
}
