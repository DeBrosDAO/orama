//go:build e2e_fleet

package clistorageglobal

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Sealing parameters (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-seal).
const (
	sealReplicas = 3
	// sealPlainBytes spans several 1024-byte pieces and ends mid-piece.
	sealPlainBytes = 5*1024 + 17
	maxReplicas    = 32
)

// slotLineRe is one line seal prints: "slot N root <64 hex>".
var slotLineRe = regexp.MustCompile(`^slot (\d+) root ([0-9a-f]{64})$`)

// sealKit is a sealing setup: seeds with mode 0600, a nonce, a plaintext.
type sealKit struct {
	dir, seed, repair, nonce, plain, out string
	data                                 []byte
}

func newSealKit(t testing.TB) sealKit {
	t.Helper()
	dir := t.TempDir()
	k := sealKit{dir: dir, nonce: strings.Repeat("5a", 32), out: filepath.Join(dir, "slots"),
		seed: secretFile(t, dir, "seed", 0x01, 0o600), repair: secretFile(t, dir, "repair", 0x02, 0o600),
		plain: filepath.Join(dir, "plain.bin"), data: make([]byte, sealPlainBytes)}
	if _, err := rand.Read(k.data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k.plain, k.data, 0o600); err != nil {
		t.Fatal(err)
	}
	return k
}

func (k sealKit) sealArgs() []string {
	return []string{"storage", "seal", "--storage-key-file", k.seed, "--repair-seed-file", k.repair,
		"--nonce", k.nonce, "--in", k.plain, "--out-dir", k.out, "--replicas", fmt.Sprint(sealReplicas)}
}

func (k sealKit) openArgs(slot int, seed, out string) []string {
	return []string{"storage", "open", "--storage-key-file", seed, "--repair-seed-file", k.repair, "--nonce", k.nonce,
		"--slot", fmt.Sprint(slot), "--in", k.slotFile(slot), "--out", out}
}

func (k sealKit) slotFile(i int) string { return filepath.Join(k.out, fmt.Sprintf("slot-%d", i)) }

func (k sealKit) seal(t testing.TB, cli *oramacli.Runner) []string {
	t.Helper()
	res := cli.MustOK(t, k.sealArgs()...)
	var roots []string
	for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		m := slotLineRe.FindStringSubmatch(l)
		if m == nil || m[1] != fmt.Sprint(len(roots)) {
			t.Fatalf("seal printed %q, want \"slot N root <hex>\" in order", l)
		}
		roots = append(roots, m[2])
	}
	if len(roots) != sealReplicas {
		t.Fatalf("seal printed %d roots, want %d", len(roots), sealReplicas)
	}
	return roots
}

// TestStorageSeal_roundTripEverySlot: seal writes one different ciphertext
// per slot and prints each piece root; open turns every slot back into the
// exact plaintext (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-seal, #orama-storage-open).
func TestStorageSeal_roundTripEverySlot(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	k := newSealKit(t)
	roots := k.seal(t, cli)
	seen := map[string]bool{}
	for i := range sealReplicas {
		blob, err := os.ReadFile(k.slotFile(i))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(blob)] || seen[roots[i]] || bytes.Contains(blob, k.data[:64]) {
			t.Errorf("slot %d repeats another slot or carries plaintext", i)
		}
		seen[string(blob)], seen[roots[i]] = true, true
		out := filepath.Join(k.dir, fmt.Sprintf("open-%d", i))
		cli.MustOK(t, k.openArgs(i, k.seed, out)...)
		if got, err := os.ReadFile(out); err != nil || !bytes.Equal(got, k.data) {
			t.Errorf("slot %d opened to %d bytes (err %v), want the %d-byte plaintext", i, len(got), err, len(k.data))
		}
	}
}

// TestStorageOpen_wrongKeyOrSlotWritesNothing: "A wrong seed, repair seed,
// or slot fails and writes nothing" (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-open),
// and so does a tampered slot file.
func TestStorageOpen_wrongKeyOrSlotWritesNothing(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	k := newSealKit(t)
	k.seal(t, cli)
	wrongSeed := secretFile(t, k.dir, "wrong-seed", 0x03, 0o600)
	tampered := filepath.Join(k.dir, "tampered")
	blob, err := os.ReadFile(k.slotFile(0))
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)/2] ^= 0xff
	if err := os.WriteFile(tampered, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"wrong seed":   k.openArgs(0, wrongSeed, filepath.Join(k.dir, "o1")),
		"wrong repair": replace(k.openArgs(0, k.seed, filepath.Join(k.dir, "o2")), "--repair-seed-file", wrongSeed),
		"wrong slot":   replace(k.openArgs(0, k.seed, filepath.Join(k.dir, "o3")), "--slot", "1"),
		"wrong nonce":  replace(k.openArgs(0, k.seed, filepath.Join(k.dir, "o4")), "--nonce", strings.Repeat("a5", 32)),
		"tampered":     replace(k.openArgs(0, k.seed, filepath.Join(k.dir, "o5")), "--in", tampered),
	}
	for label, args := range cases {
		res := run(t, cli, args...)
		if res.Exit == exitOK {
			t.Errorf("open with a %s succeeded", label)
		}
		out := args[len(args)-1]
		if _, err := os.Stat(out); err == nil {
			t.Errorf("open with a %s wrote %s", label, out)
		}
	}
}

// TestStorageRewrap_rebuildsAnotherSlot: rewrap turns slot 0 into slot 2 with
// the repair seed alone, byte for byte what seal wrote for slot 2
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-rewrap).
func TestStorageRewrap_rebuildsAnotherSlot(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	k := newSealKit(t)
	k.seal(t, cli)
	out := filepath.Join(k.dir, "rewrapped-2")
	cli.MustOK(t, "storage", "rewrap", "--repair-seed-file", k.repair, "--nonce", k.nonce,
		"--from", "0", "--to", "2", "--in", k.slotFile(0), "--out", out)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(k.slotFile(2))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("rewrap of slot 0 into slot 2 differs from the slot 2 seal wrote")
	}
	// A slot rewrapped from the wrong --from is not the slot: rewrap has no
	// way to tell (it holds no plaintext key), but the result must never open.
	bad := filepath.Join(k.dir, "wrong-from")
	if res := run(t, cli, "storage", "rewrap", "--repair-seed-file", k.repair, "--nonce", k.nonce,
		"--from", "1", "--to", "2", "--in", k.slotFile(0), "--out", bad); res.Exit != exitOK {
		return
	}
	opened := filepath.Join(k.dir, "wrong-from-opened")
	args := replace(k.openArgs(2, k.seed, opened), "--in", bad)
	infra.ExpectRefused(t, run(t, cli, args...), "cannot be opened")
	if _, err := os.Stat(opened); err == nil {
		t.Error("a slot rewrapped from the wrong source opened to a plaintext")
	}
}

// TestStorageSeal_refusals: seeds readable by others are refused (they must
// be 0600), a malformed nonce or replica count is a bad value (exit 2), and
// a missing required flag is a usage error.
func TestStorageSeal_refusals(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	k := newSealKit(t)
	loose := secretFile(t, k.dir, "loose-seed", 0x04, 0o644)
	infra.ExpectRefused(t, run(t, cli, replace(k.sealArgs(), "--storage-key-file", loose)...), "chmod 600")
	for label, args := range map[string][]string{
		"short nonce":    replace(k.sealArgs(), "--nonce", "abcd"),
		"nonce not hex":  replace(k.sealArgs(), "--nonce", strings.Repeat("zz", 32)),
		"zero replicas":  replace(k.sealArgs(), "--replicas", "0"),
		"too many slots": replace(k.sealArgs(), "--replicas", fmt.Sprint(maxReplicas+1)),
		"no input":       without(k.sealArgs(), "--in"),
		"no out dir":     without(k.sealArgs(), "--out-dir"),
	} {
		if res := run(t, cli, args...); res.Exit != exitUsage {
			t.Errorf("seal with %s: exit %d, want %d\n%s", label, res.Exit, exitUsage, output(res))
		}
	}
	if _, err := os.Stat(k.out); err == nil {
		t.Errorf("refused seals created %s", k.out)
	}
}
