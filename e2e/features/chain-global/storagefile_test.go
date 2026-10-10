//go:build e2e_fleet

package chainglobal

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// sealedReplicas is the replica count the round trip seals.
const sealedReplicas = 3

// The CLI's own refusal texts: storagefile.ErrNotForKey (a slot opened as
// another slot), storagecmd secretFile (a seed file others can read) and
// storageclient.ErrNotFound (a deal the chain does not hold).
const (
	notForKey       = "storage file cannot be opened with this key"
	seedModeRefused = "chmod 600 it"
	dealNotFound    = "not found on chain"
)

var sealedRoot = regexp.MustCompile(`(?m)^slot (\d+) root ([0-9a-f]{64})$`)

// sealFixture is a plaintext, its two seeds and a nonce, in a private dir.
type sealFixture struct {
	dir, plain, seed, repair, nonce string
	content                         []byte
}

func newSealFixture(t *testing.T) sealFixture {
	t.Helper()
	dir := t.TempDir()
	f := sealFixture{dir: dir, plain: filepath.Join(dir, "plain.bin"), seed: filepath.Join(dir, "seed"),
		repair: filepath.Join(dir, "repair"), nonce: randomHex(t, 32), content: make([]byte, 5000)}
	if _, err := rand.Read(f.content); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{f.plain: f.content, f.seed: []byte(randomHex(t, 32)), f.repair: []byte(randomHex(t, 32))} {
		if err := os.WriteFile(path, data, secretFileMode); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// TestStorageFiles_sealOpenRewrapRoundTrip: `orama storage seal` writes one
// different ciphertext per slot and prints each piece root; `open` of any
// slot returns the plaintext; `rewrap` turns slot 0 into exactly slot 2
// with the repair seed only; a wrong seed or slot fails and writes nothing;
// a seed file other users can read is refused (docs/whitepaper/technical-reference/vol2/41-storage-deals.md "Client side").
func TestStorageFiles_sealOpenRewrapRoundTrip(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	f := newSealFixture(t)
	out := filepath.Join(f.dir, "sealed")
	res := infra.Run(t, cli, "storage", "seal", "--in", f.plain, "--nonce", f.nonce, "--out-dir", out, "--storage-key-file", f.seed,
		"--repair-seed-file", f.repair, "--replicas", fmt.Sprint(sealedReplicas))
	infra.ExpectExit(t, res, infra.ExitOK)
	if roots := sealedRoot.FindAllStringSubmatch(res.Stdout, -1); len(roots) != sealedReplicas || roots[0][2] == roots[1][2] {
		t.Fatalf("seal printed roots %v, want %d distinct", roots, sealedReplicas)
	}
	slot := func(i int) []byte {
		b, err := os.ReadFile(filepath.Join(out, fmt.Sprintf("slot-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	opened := filepath.Join(f.dir, "opened")
	infra.ExpectExit(t, infra.Run(t, cli, "storage", "open", "--in", filepath.Join(out, "slot-1"), "--nonce", f.nonce, "--slot", "1",
		"--out", opened, "--storage-key-file", f.seed, "--repair-seed-file", f.repair), infra.ExitOK)
	if got, _ := os.ReadFile(opened); !bytes.Equal(got, f.content) {
		t.Errorf("open of slot 1 did not return the plaintext")
	}
	rewrapped := filepath.Join(f.dir, "rewrapped")
	infra.ExpectExit(t, infra.Run(t, cli, "storage", "rewrap", "--in", filepath.Join(out, "slot-0"), "--from", "0", "--to", "2",
		"--nonce", f.nonce, "--out", rewrapped, "--repair-seed-file", f.repair), infra.ExitOK)
	if got, _ := os.ReadFile(rewrapped); !bytes.Equal(got, slot(2)) {
		t.Errorf("rewrap of slot 0 into slot 2 differs from seal's slot 2")
	}
	wrongOut := filepath.Join(f.dir, "wrong")
	infra.ExpectRefused(t, infra.Run(t, cli, "storage", "open", "--in", filepath.Join(out, "slot-1"), "--nonce", f.nonce, "--slot", "2",
		"--out", wrongOut, "--storage-key-file", f.seed, "--repair-seed-file", f.repair), notForKey)
	if _, err := os.Stat(wrongOut); err == nil {
		t.Errorf("a failed open wrote %s", wrongOut)
	}
	if err := os.Chmod(f.seed, 0o644); err != nil {
		t.Fatal(err)
	}
	infra.ExpectRefused(t, infra.Run(t, cli, "storage", "open", "--in", filepath.Join(out, "slot-1"), "--nonce", f.nonce, "--slot", "1",
		"--out", wrongOut, "--storage-key-file", f.seed, "--repair-seed-file", f.repair), seedModeRefused)
}

// TestStorageFiles_putAndGetCheckTheChainFirst: `orama storage put` checks
// every slot file's root against the deal on chain before sending a byte, and
// `get` needs the deal's accepted slots; for a deal that does not exist both
// fail and nothing is uploaded or written. (No deal can be funded on the run
// chain and orama-global is not deployed, so a real upload is blocked.) The
// chain RPC is reached through an SSH tunnel to the node's loopback 31001.
func TestStorageFiles_putAndGetCheckTheChainFirst(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	cli := harness.CLI(t)
	rpc := "http://" + c.Tunnel(t, c.Node(t, 0), chain.RPCPort)
	f := newSealFixture(t)
	out := filepath.Join(f.dir, "sealed")
	infra.ExpectExit(t, infra.Run(t, cli, "storage", "seal", "--in", f.plain, "--nonce", f.nonce, "--out-dir", out,
		"--storage-key-file", f.seed, "--repair-seed-file", f.repair), infra.ExitOK)
	put := infra.Run(t, cli, "storage", "put", "--deal-id", "987654321", "--dir", out, "--rpc", rpc, "--wait", "15s")
	infra.ExpectRefused(t, put, "987654321", dealNotFound)
	if bytes.Contains([]byte(put.Stdout), []byte("uploaded")) {
		t.Errorf("put reported an upload for a deal that does not exist: %s", put.Stdout)
	}
	got := filepath.Join(f.dir, "got")
	infra.ExpectRefused(t, infra.Run(t, cli, "storage", "get", "--deal-id", "987654321", "--out", got, "--rpc", rpc,
		"--storage-key-file", f.seed, "--repair-seed-file", f.repair), dealNotFound)
	if _, err := os.Stat(got); err == nil {
		t.Errorf("a failed get wrote %s", got)
	}
	empty := t.TempDir()
	infra.ExpectExit(t, infra.Run(t, cli, "storage", "put", "--deal-id", "1", "--dir", empty, "--rpc", rpc), infra.ExitUsage, "holds no slot-0 file")
}
