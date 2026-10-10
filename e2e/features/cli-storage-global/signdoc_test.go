//go:build e2e_fleet

package clistorageglobal

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// Sizes of a storage proof (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-prove).
const (
	proofLeafBytes    = 1024
	proofSiblingBytes = 32
)

// txCommand is one chain transaction command and the Msg type its sign
// document must carry (core/pkg/clusterreg *TypeURL).
type txCommand struct {
	args    []string
	typeURL string
}

// txCommands are valid invocations of every transaction command; without
// --node each prints its sign document and submits nothing.
func txCommands(t testing.TB, baseDomain string) []txCommand {
	t.Helper()
	nonce := strings.Repeat("ab", 32)
	piece := strings.Repeat("cd", 32) + ":1024"
	op := func(args ...string) []string { return append(args, signerFlags("--operator")...) }
	sg := func(args ...string) []string { return append(args, signerFlags("--signer")...) }
	return []txCommand{
		{op("cluster", "register-onchain", "--id", "e2e-cluster", "--base-domain", baseDomain, "--endpoint", "https://"+baseDomain), "/orama.nodes.v1.MsgRegisterCluster"},
		{op("cluster", "retire-onchain", "--id", "e2e-cluster"), "/orama.nodes.v1.MsgRetireCluster"},
		{op("global", "bond", "--id", nodeID, "--role", "storage", "--amount", "1000"), "/orama.nodes.v1.MsgBondNode"},
		{op("global", "unbond", "--id", nodeID, "--role", "storage", "--amount", "1000"), "/orama.nodes.v1.MsgUnbondNode"},
		{op("global", "capacity", "--id", nodeID, "--bytes", "1048576"), "/orama.nodes.v1.MsgDeclareCapacity"},
		{op("global", "retire", "--id", nodeID), "/orama.nodes.v1.MsgRetireNode"},
		{sg("storage", "create", "--class", "public-pin", "--nonce", nonce, "--price", "10", "--duration-epochs", "5", "--piece", piece), "/orama.storage.v1.MsgCreateDeal"},
		{sg("storage", "extend", "--deal-id", "1", "--extra-epochs", "2"), "/orama.storage.v1.MsgExtendDeal"},
		{sg("storage", "accept", "--id", nodeID, "--deal-id", "1", "--slot", "0"), "/orama.storage.v1.MsgAcceptDeal"},
		{sg("storage", "decline", "--id", nodeID, "--deal-id", "1", "--slot", "0", "--reason", "e2e"), "/orama.storage.v1.MsgDeclineDeal"},
		{sg("storage", "grant", "--grantee", hotKey, "--spend-limit", "1000", "--max-piece-bytes", "1048576", "--max-duration-epochs", "10"), "/orama.storage.v1.MsgGrantDealAuthorization"},
		{sg("storage", "revoke", "--grantee", hotKey), "/orama.storage.v1.MsgRevokeDealAuthorization"},
		{sg("storage", "prove", "--id", nodeID, "--file", proofFile(t)), "/orama.storage.v1.MsgSubmitProofs"},
	}
}

// proofFile writes one well-formed proof (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-prove).
func proofFile(t testing.TB) string {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{{
		"deal_id": 1, "slot": 0, "leaf_index": 0,
		"leaf":     hex.EncodeToString(make([]byte, proofLeafBytes)),
		"siblings": []string{hex.EncodeToString(make([]byte, proofSiblingBytes))},
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "proofs.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSignDoc_printedNotSubmitted: without --node every chain transaction
// command prints the SIGN_MODE_DIRECT sign document and submits nothing
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-cluster-register-onchain, #orama-global-bond,
// #orama-storage-create, ...). The document names the command's Msg type,
// the chain id, the signer and its public key. It runs on a machine with no
// wallet: printing a sign document must not need one.
func TestSignDoc_printedNotSubmitted(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := cliNoWallet(t)
	pub, err := hex.DecodeString(pubKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range txCommands(t, f.State.BaseDomain) {
		t.Run(strings.Join(c.args[:2], "_"), func(t *testing.T) {
			t.Parallel()
			doc := signDoc(t, run(t, cli, c.args...))
			expectContains(t, strings.Join(c.args[:2], " "), doc,
				[]byte(c.typeURL), []byte(signChainID), []byte(operator), pub)
		})
	}
}

// TestSignDoc_deterministic: the same command line prints the same document
// twice, so an operator can sign it on another machine and trust it.
func TestSignDoc_deterministic(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := cliNoWallet(t)
	for _, c := range txCommands(t, f.State.BaseDomain)[:4] {
		a := signDoc(t, run(t, cli, c.args...))
		b := signDoc(t, run(t, cli, c.args...))
		if hex.EncodeToString(a) != hex.EncodeToString(b) {
			t.Errorf("orama %v printed two different sign documents", c.args[:2])
		}
	}
}

// TestSignDoc_jsonFlagDoesNotBreakIt: --json is accepted everywhere; these
// commands print text, and must still print the document.
func TestSignDoc_jsonFlagDoesNotBreakIt(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := txCommands(t, f.State.BaseDomain)[1]
	signDoc(t, run(t, cliNoWallet(t), append(c.args, "--json")...))
}
