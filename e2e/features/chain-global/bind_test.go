//go:build e2e_fleet

package chainglobal

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// secretFileMode is how a service secret file is written (owner only).
const secretFileMode = 0o600

// binding is what `orama global bind` prints.
type binding struct {
	Service   string `json:"service"`
	KeyType   string `json:"key_type"`
	Pubkey    string `json:"pubkey"`
	Signature string `json:"signature"`
}

// writeSecret writes a service secret, hex, into a private test directory.
func writeSecret(t *testing.T, name string, secret []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)), secretFileMode); err != nil {
		t.Fatal(err)
	}
	return path
}

// bind runs `orama global bind` and decodes the binding; the file it names
// is written next to the key for `orama global register --binding`.
func bind(t *testing.T, chainID, operator, service, keyFile, keyType string) (binding, string) {
	t.Helper()
	res := infra.Run(t, harness.CLI(t), "global", "bind", "--chain-id", chainID, "--operator", operator,
		"--service", service, "--key-file", keyFile, "--key-type", keyType)
	infra.ExpectExit(t, res, infra.ExitOK, "binding signed; not submitted")
	var b binding
	if err := json.Unmarshal([]byte(res.Stdout), &b); err != nil {
		t.Fatalf("orama global bind printed no binding JSON: %v\n%s", err, res.Stdout)
	}
	out := filepath.Join(filepath.Dir(keyFile), service+".binding.json")
	if err := os.WriteFile(out, []byte(res.Stdout), secretFileMode); err != nil {
		t.Fatal(err)
	}
	return b, out
}

func decodeHex(t *testing.T, what, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("%s %q is not hex: %v", what, s, err)
	}
	return b
}

// TestGlobalBind_signsTheDocumentedStatement: `orama global bind` signs
// orama-global-bind-v1|chain-id|operator|service|hex(pubkey) (docs/CHAIN.md
// "x/nodes" Messages) with an ed25519 seed and with a secp256k1 secret, prints
// only the public key and signature, and each signature verifies here for
// exactly that statement and no other chain id.
func TestGlobalBind_signsTheDocumentedStatement(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	operator := c.Validator(t, c.Node(t, 0)).Address
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ed, _ := bind(t, c.ID, operator, "relay", writeSecret(t, "ed.key", edPriv.Seed()), "ed25519")
	statement := chain.BindingSignBytes(c.ID, operator, "relay", decodeHex(t, "pubkey", ed.Pubkey))
	if ed.KeyType != "ed25519" || !ed25519.Verify(decodeHex(t, "pubkey", ed.Pubkey), statement, decodeHex(t, "signature", ed.Signature)) {
		t.Errorf("ed25519 binding %+v does not verify for %q", ed, statement)
	}
	other := chain.BindingSignBytes(c.ID+"-other", operator, "relay", decodeHex(t, "pubkey", ed.Pubkey))
	if ed25519.Verify(decodeHex(t, "pubkey", ed.Pubkey), other, decodeHex(t, "signature", ed.Signature)) {
		t.Errorf("the binding also verifies for another chain id")
	}
	secp, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := bind(t, c.ID, operator, "storage", writeSecret(t, "secp.key", crypto.FromECDSA(secp)), "secp256k1")
	pub := decodeHex(t, "pubkey", sb.Pubkey)
	digest := sha256.Sum256(chain.BindingSignBytes(c.ID, operator, "storage", pub))
	if sb.KeyType != "secp256k1" || len(pub) != 33 || !crypto.VerifySignature(pub, digest[:], decodeHex(t, "signature", sb.Signature)) {
		t.Errorf("secp256k1 binding %+v does not verify over the SHA-256 digest", sb)
	}
}

// TestGlobalBind_refusals: missing required flags and an ambiguous raw
// 32-byte file without --key-type are usage errors; a key file that does not
// exist is a failure; a service name outside [a-z][a-z0-9_-]{0,31} is refused.
func TestGlobalBind_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	cli := harness.CLI(t)
	operator := c.Validator(t, c.Node(t, 0)).Address
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	key := writeSecret(t, "raw.key", seed)
	infra.ExpectExit(t, infra.Run(t, cli, "global", "bind", "--chain-id", c.ID, "--operator", operator, "--service", "relay"),
		infra.ExitUsage, "required")
	infra.ExpectExit(t, infra.Run(t, cli, "global", "bind", "--chain-id", c.ID, "--operator", operator, "--service", "relay", "--key-file", key),
		infra.ExitUsage, "--key-type")
	infra.ExpectExit(t, infra.Run(t, cli, "global", "bind", "--chain-id", c.ID, "--operator", operator, "--service", "relay",
		"--key-file", filepath.Join(t.TempDir(), "absent.key"), "--key-type", "ed25519"), infra.ExitFailure)
	infra.ExpectRefused(t, infra.Run(t, cli, "global", "bind", "--chain-id", c.ID, "--operator", operator, "--service", "Relay!",
		"--key-file", key, "--key-type", "ed25519"), `service "Relay!" must match`)
}

// TestGroupCommands_listTheirSubcommands: `orama global` and `orama storage`
// list the subcommands docs/CLI_REFERENCE.md names for them.
func TestGroupCommands_listTheirSubcommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	groups := map[string][]string{
		"global":  {"bind", "bond", "capacity", "register", "retire", "stage-oramad", "unbond"},
		"storage": {"accept", "create", "decline", "extend", "get", "grant", "open", "prove", "put", "revoke", "rewrap", "seal"},
	}
	for group, subs := range groups {
		res := infra.Run(t, cli, group, "--help")
		infra.ExpectExit(t, res, infra.ExitOK, subs...)
	}
}
