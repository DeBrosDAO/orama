//go:build e2e_fleet

package chain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Key is a keyring entry on one node. KeyringDir empty is the chain home's
// own keyring, where the validator operator key lives.
type Key struct {
	Node       fleet.Node
	Name       string
	KeyringDir string
	Address    string
}

// addrPattern is an orama account address (bech32, prefix orama; the same
// format e2e/scripts/chain-deploy.sh validates).
var addrPattern = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{38}$`)

// valoperPattern is an operator address, prefix oramavaloper.
var valoperPattern = regexp.MustCompile(`^oramavaloper1[02-9ac-hj-np-z]{38}$`)

// keyNamePattern is what a throwaway key may be called.
var keyNamePattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var (
	validatorCache sync.Map // chain id/node name -> Key
	valoperCache   sync.Map // chain id/node/keyring/name -> oramavaloper address
)

// keyringFlags are the flags that select k's keyring.
func (k Key) keyringFlags() []string {
	flags := []string{"--keyring-backend", "test"}
	if k.KeyringDir != "" {
		flags = append(flags, "--keyring-dir", k.KeyringDir)
	}
	return flags
}

// Validator is the operator key of the validator co-hosted on n. It earns
// the epoch rewards (docs/CHAIN.md "Rewards, paid on capped power"), so it is
// the only kind of account a run chain can fund (see funds.go).
func (c *Chain) Validator(t testing.TB, n fleet.Node) Key {
	t.Helper()
	if c.F.State.IsStagenet() {
		harness.SkipNotApplicable(t, "a stagenet node's test keyring does not hold the validator operator key (chain-deploy.sh creates and funds it on a run chain only), so a test that signs with it cannot apply")
	}
	if v, ok := validatorCache.Load(c.ID + "/" + n.Name); ok {
		return v.(Key)
	}
	k := Key{Node: n, Name: ValidatorKey}
	k.Address = c.showAddress(t, k, "acc")
	validatorCache.Store(c.ID+"/"+n.Name, k)
	return k
}

// Valoper is k's operator (oramavaloper) address.
func (c *Chain) Valoper(t testing.TB, k Key) string {
	t.Helper()
	id := c.ID + "/" + k.Node.Name + "/" + k.KeyringDir + "/" + k.Name
	if v, ok := valoperCache.Load(id); ok {
		return v.(string)
	}
	addr := c.showAddress(t, k, "val")
	valoperCache.Store(id, addr)
	return addr
}

func (c *Chain) showAddress(t testing.TB, k Key, bech string) string {
	t.Helper()
	args := append([]string{"keys", "show", k.Name, "-a", "--bech", bech}, k.keyringFlags()...)
	out := c.Run(t, k.Node, QueryBudget, c.OramadCmd(args...))
	addr := strings.TrimSpace(out.Stdout)
	want := addrPattern
	if bech == "val" {
		want = valoperPattern
	}
	if out.Exit != 0 || !want.MatchString(addr) {
		t.Fatalf("%s: key %s has no %s address (exit %d): %q %s", k.Node.Name, k.Name, bech, out.Exit, addr, out.Stderr)
	}
	return addr
}

// PubKeyHex is k's compressed secp256k1 public key, hex (the --pubkey the
// orama CLI's chain commands take).
func (c *Chain) PubKeyHex(t testing.TB, k Key) string {
	t.Helper()
	args := append([]string{"keys", "show", k.Name, "--output", "json"}, k.keyringFlags()...)
	out := c.Run(t, k.Node, QueryBudget, c.OramadCmd(args...))
	var shown struct {
		PubKey string `json:"pubkey"`
	}
	if out.Exit != 0 || json.Unmarshal([]byte(out.Stdout), &shown) != nil {
		t.Fatalf("%s: keys show %s: exit %d %s", k.Node.Name, k.Name, out.Exit, out.Stderr)
	}
	var pk struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(shown.PubKey), &pk); err != nil {
		t.Fatalf("%s: key %s pubkey %q: %v", k.Node.Name, k.Name, shown.PubKey, err)
	}
	raw, err := base64.StdEncoding.DecodeString(pk.Key)
	if err != nil || len(raw) != 33 {
		t.Fatalf("%s: key %s is not a compressed secp256k1 key: %q", k.Node.Name, k.Name, pk.Key)
	}
	return hex.EncodeToString(raw)
}

// NewKey creates a throwaway secp256k1 key in a keyring of its own on n (a
// fresh directory, owned by the chain user, removed at cleanup). The key has
// never received anything, so its account does not exist on chain. Its
// mnemonic is never printed (--no-backup).
func (c *Chain) NewKey(t testing.TB, n fleet.Node, name string) Key {
	t.Helper()
	if !keyNamePattern.MatchString(name) {
		t.Fatalf("key name %q must match %s", name, keyNamePattern)
	}
	dir := "/tmp/e2e-chainkeys-" + randomHex(t, 8)
	t.Cleanup(func() { c.cleanupDir(t, n, dir) })
	mk := c.Run(t, n, QueryBudget, fmt.Sprintf("install -d -o %s -g %s -m 0700 %s", ServiceUser, ServiceUser, dir))
	if mk.Exit != 0 {
		t.Fatalf("%s: failed to create keyring dir %s: %s", n.Name, dir, mk.Stderr)
	}
	k := Key{Node: n, Name: name, KeyringDir: dir}
	args := append([]string{"keys", "add", name, "--no-backup"}, k.keyringFlags()...)
	add := c.Run(t, n, QueryBudget, c.OramadCmd(args...)+" >/dev/null")
	if add.Exit != 0 {
		t.Fatalf("%s: failed to add key %s: %s", n.Name, name, c.F.Redact(add.Stderr))
	}
	k.Address = c.showAddress(t, k, "acc")
	return k
}

// cleanupDir removes a directory this package created under /tmp.
func (c *Chain) cleanupDir(t testing.TB, n fleet.Node, dir string) {
	t.Helper()
	if !strings.HasPrefix(dir, "/tmp/e2e-chain") {
		t.Errorf("refusing to remove %s: not a directory this package creates", dir)
		return
	}
	out, err := c.cleanupRun(t, n, "rm -rf -- "+fleet.ShellQuote(dir))
	if err != nil || out.Exit != 0 {
		t.Errorf("%s: failed to remove %s (exit %d): %v %s", n.Name, dir, out.Exit, err, out.Stderr)
	}
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to read randomness: %v", err)
	}
	return hex.EncodeToString(b)
}

// UniqueID is a short random id for on-chain records a test creates
// (node ids, cluster ids, subdenoms), prefixed so a reader knows its origin.
func UniqueID(t testing.TB, prefix string) string {
	t.Helper()
	return prefix + randomHex(t, 5)
}
