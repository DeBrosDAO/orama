package storagefile

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pieceroot"
)

func TestPrepareOpenRoundTrip(t *testing.T) {
	seed := bytes.Repeat([]byte{1}, 32)
	repair := bytes.Repeat([]byte{2}, 32)
	nonce := bytes.Repeat([]byte{3}, DealNonceLen)
	plain := []byte("a private file")
	slots, err := Prepare(seed, repair, nonce, 3, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 {
		t.Fatalf("slots %d", len(slots))
	}
	seen := map[string]struct{}{}
	for i, slot := range slots {
		if slot.Index != uint32(i) {
			t.Fatalf("index %d", slot.Index)
		}
		if _, ok := seen[string(slot.Bytes)]; ok {
			t.Fatal("two slots share ciphertext")
		}
		seen[string(slot.Bytes)] = struct{}{}
		c, err := pieceroot.Commit(slot.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(c.Root, slot.Root) || c.RealLeafCount != slot.RealLeafCount {
			t.Fatal("root drifted from the piece commitment")
		}
		got, err := Open(seed, repair, nonce, slot.Index, slot.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("open %q", got)
		}
	}
	if _, err := Open(bytes.Repeat([]byte{9}, 32), repair, nonce, 0, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong seed %v", err)
	}
	if _, err := Open(seed, bytes.Repeat([]byte{9}, 32), nonce, 0, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong repair %v", err)
	}
	if _, err := Open(seed, repair, nonce, 1, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong slot %v", err)
	}
	empty, err := Prepare(seed, repair, nonce, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(seed, repair, nonce, 0, empty[0].Bytes)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty %q %v", got, err)
	}
	if _, err := Prepare(seed[:16], repair, nonce, 1, plain); err == nil {
		t.Fatal("short seed was accepted")
	}
}

// abandonSeedHex is the BIP-39 seed of the all-"abandon ... about" phrase and
// abandonStorageKeyHex the orama-storage-v1 branch RootWallet publishes for it
// (rootwallet docs/CRYPTO_ARCHITECTURE.md, HKDF Seed Branches).
const (
	abandonSeedHex       = "5eb00bbddcf069084889a8ab9155568165f5c453ccb85e70811aaed6f6da5fc19a5ac40b389cd370d086206dec8aa6c43daea6690f20ad3d8d48b2d2ce9e38e4"
	abandonStorageKeyHex = "9b164e82a47f07b828399215348b11067a247f0f996a8e8f1155b6b596122052"
)

func TestDeriveStorageKey_matchesRootWalletVector(t *testing.T) {
	seed, err := hex.DecodeString(abandonSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DeriveStorageKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != abandonStorageKeyHex {
		t.Fatalf("storage key %x", got)
	}
}

func TestDeriveStorageKey_emptySeed(t *testing.T) {
	if _, err := DeriveStorageKey(nil); err == nil {
		t.Fatal("empty seed was accepted")
	}
}

func TestPrepareOpen_wrapKeyIsTheRootWalletBranch(t *testing.T) {
	seed, _ := hex.DecodeString(abandonSeedHex)
	key, err := DeriveStorageKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	repair := bytes.Repeat([]byte{2}, 32)
	nonce := bytes.Repeat([]byte{3}, DealNonceLen)
	slots, err := Prepare(key, repair, nonce, 1, []byte("recoverable"))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := applyOuter(repair, nonce, 0, slots[0].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	wantKey, _ := hex.DecodeString(abandonStorageKeyHex)
	got, err := openInner(wantKey, inner)
	if err != nil || string(got) != "recoverable" {
		t.Fatalf("the published branch does not open the file: %q %v", got, err)
	}
}

func TestPrepareOpen_refuseStorageKeyOfWrongLength(t *testing.T) {
	repair := bytes.Repeat([]byte{2}, 32)
	nonce := bytes.Repeat([]byte{3}, DealNonceLen)
	good := bytes.Repeat([]byte{1}, StorageKeyLen)
	slots, err := Prepare(good, repair, nonce, 1, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 31, 33, 64} {
		key := bytes.Repeat([]byte{1}, n)
		if _, err := Prepare(key, repair, nonce, 1, []byte("x")); err == nil {
			t.Fatalf("Prepare accepted a %d-byte storage key", n)
		}
		if _, err := Open(key, repair, nonce, 0, slots[0].Bytes); err == nil || errors.Is(err, ErrNotForKey) {
			t.Fatalf("Open with a %d-byte key returned %v", n, err)
		}
	}
}
