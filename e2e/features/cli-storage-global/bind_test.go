//go:build e2e_fleet

package clistorageglobal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// bindPrefix is the domain separator every binding signs
// (core/pkg/globalbind Prefix, chain/x/nodes/types.BindingPrefix).
const bindPrefix = "orama-global-bind-v1"

// bindService is a service name a binding may carry.
const bindService = "provider"

// binding is what `orama global bind` prints.
type binding struct {
	Service   string `json:"service"`
	KeyType   string `json:"key_type"`
	Pubkey    string `json:"pubkey"`
	Signature string `json:"signature"`
}

// cometKeyFile writes a CometBFT ed25519 priv_key JSON (seed||pubkey).
func cometKeyFile(t testing.TB, dir string) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(`{"priv_key":{"type":"tendermint/PrivKeyEd25519","value":%q}}`, base64.StdEncoding.EncodeToString(priv))
	path := filepath.Join(dir, "priv_validator_key.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pub
}

func bindArgs(keyFile, chainID string) []string {
	return []string{"global", "bind", "--chain-id", chainID, "--operator", operator, "--service", bindService, "--key-file", keyFile}
}

// TestGlobalBind_signatureVerifies: bind signs
// "orama-global-bind-v1|chain-id|operator|service|hex(pubkey)" with the
// service key, prints the public key and signature, keeps the private key in
// its file, and submits nothing (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-global).
func TestGlobalBind_signatureVerifies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyFile, pub := cometKeyFile(t, dir)
	before, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	res := cliNoWallet(t).MustOK(t, bindArgs(keyFile, signChainID)...)
	var b binding
	if err := json.Unmarshal([]byte(res.Stdout), &b); err != nil {
		t.Fatalf("bind printed no binding JSON: %v\n%s", err, res.Stdout)
	}
	sig, err := hex.DecodeString(b.Signature)
	if err != nil {
		t.Fatalf("signature is not hex: %v", err)
	}
	if b.Service != bindService || b.KeyType != "ed25519" || b.Pubkey != hex.EncodeToString(pub) {
		t.Fatalf("binding %+v, want service %s, ed25519, pubkey %x", b, bindService, pub)
	}
	msg := strings.Join([]string{bindPrefix, signChainID, operator, bindService, b.Pubkey}, "|")
	if !ed25519.Verify(pub, []byte(msg), sig) {
		t.Error("the binding signature does not verify over the documented statement")
	}
	if strings.Contains(res.Stdout+res.Stderr, base64.StdEncoding.EncodeToString(before)) {
		t.Error("bind printed the private key")
	}
	if !strings.Contains(res.Stderr, "not submitted") {
		t.Errorf("bind did not say it submitted nothing: %q", res.Stderr)
	}
	if after, _ := os.ReadFile(keyFile); !bytes.Equal(before, after) {
		t.Error("bind changed the key file")
	}
}

// TestGlobalBind_refusals: every required flag, a raw 32-byte key without
// --key-type (it could be either curve), a bad service name and a key file
// that does not exist are refused.
func TestGlobalBind_refusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyFile, _ := cometKeyFile(t, dir)
	raw := secretFile(t, dir, "raw32", 0x07, 0o600)
	cli := cliNoWallet(t)
	for label, args := range map[string][]string{
		"no chain id":      without(bindArgs(keyFile, signChainID), "--chain-id"),
		"no operator":      without(bindArgs(keyFile, signChainID), "--operator"),
		"no service":       without(bindArgs(keyFile, signChainID), "--service"),
		"no key file":      without(bindArgs(keyFile, signChainID), "--key-file"),
		"ambiguous raw 32": bindArgs(raw, signChainID),
		"bad service":      replace(bindArgs(keyFile, signChainID), "--service", "Provider!"),
		"bad operator":     replace(bindArgs(keyFile, signChainID), "--operator", "orama1notanaccount"),
	} {
		if res := run(t, cli, args...); res.Exit != exitUsage {
			t.Errorf("bind with %s: exit %d, want %d\n%s", label, res.Exit, exitUsage, output(res))
		}
	}
	infra.ExpectRefused(t, run(t, cli, bindArgs(filepath.Join(dir, "absent"), signChainID)...), "service key")
	cli.MustOK(t, append(bindArgs(raw, signChainID), "--key-type", "secp256k1")...)
}

// TestGlobalRegister_bindingForThisChainOnly: register builds MsgRegisterNode
// from bindings bind wrote and prints its sign document; a binding signed for
// another chain id does not verify and is refused
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-global-register).
func TestGlobalRegister_bindingForThisChainOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyFile, pub := cometKeyFile(t, dir)
	cli := cliNoWallet(t)
	write := func(name, chainID string) string {
		out := cli.MustOK(t, bindArgs(keyFile, chainID)...).Stdout
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good, other := write("good.json", signChainID), write("other.json", signChainID+"-other")
	// A node's hot key is proved by a "hot-key" binding, a secp256k1 key whose
	// account is the --hot-key (clusterreg.checkHotKeyBinding).
	hotSecret := secretFile(t, dir, "hot-secret", 0x09, 0o600)
	hotBinding := cli.MustOK(t, "global", "bind", "--chain-id", signChainID, "--operator", operator,
		"--service", clusterreg.HotKeyService, "--key-type", "secp256k1", "--key-file", hotSecret).Stdout
	var hb binding
	if err := json.Unmarshal([]byte(hotBinding), &hb); err != nil {
		t.Fatalf("bind printed no binding JSON: %v\n%s", err, hotBinding)
	}
	hotPub, err := hex.DecodeString(hb.Pubkey)
	if err != nil {
		t.Fatal(err)
	}
	hot, err := clusterreg.AccountAddressOf(hotPub)
	if err != nil {
		t.Fatal(err)
	}
	hotFile := filepath.Join(dir, "hot.json")
	if err := os.WriteFile(hotFile, []byte(hotBinding), 0o600); err != nil {
		t.Fatal(err)
	}
	args := func(hotKeyAccount string, bindings ...string) []string {
		a := withSigner("--operator", "global", "register", "--id", nodeID, "--role", "storage",
			"--hot-key", hotKeyAccount, "--endpoint", "https://e2e.example.com")
		for _, b := range bindings {
			a = append(a, "--binding", b)
		}
		return a
	}
	doc := signDoc(t, run(t, cli, args(hot, good, hotFile)...))
	expectContains(t, "global register", doc, []byte("/orama.nodes.v1.MsgRegisterNode"), []byte(hot), pub)
	for label, a := range map[string][]string{
		"other chain binding":             args(hot, other, hotFile),
		"no hot-key binding":              args(hot, good),
		"hot binding for a wrong account": args(hotKey, good, hotFile),
		"hot key is operator":             args(operator, good, hotFile),
		"no role":                         without(args(hot, good, hotFile), "--role"),
		"binding not json":                args(hot, proofFileNotBinding(t, dir), hotFile),
	} {
		if res := run(t, cli, a...); res.Exit != exitUsage {
			t.Errorf("register with %s: exit %d, want %d\n%s", label, res.Exit, exitUsage, output(res))
		}
	}
}

func proofFileNotBinding(t testing.TB, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "not-binding.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
