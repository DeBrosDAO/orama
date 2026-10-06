package storagekey

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

type vector struct {
	RepairSeed string `json:"repair_seed"`
	DealNonce  string `json:"deal_nonce"`
	Slot       uint32 `json:"slot"`
	Input      string `json:"input"`
	Output     string `json:"output"`
}

func TestApply_matchesTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/outer_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Cases []vector }
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Cases) < 5 {
		t.Fatalf("vectors: %v, %d cases", err, len(doc.Cases))
	}
	for i, c := range doc.Cases {
		seed, _ := hex.DecodeString(c.RepairSeed)
		nonce, _ := hex.DecodeString(c.DealNonce)
		in, _ := hex.DecodeString(c.Input)
		want, _ := hex.DecodeString(c.Output)
		got, err := Apply(seed, nonce, c.Slot, in)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d: output differs", i)
		}
	}
}

func TestRewrap_turnsOneSlotIntoAnotherForLargeBodies(t *testing.T) {
	seed := bytes.Repeat([]byte{4}, 32)
	nonce := bytes.Repeat([]byte{5}, 32)
	inner := bytes.Repeat([]byte("inner"), 200_000)
	slot1, err := Apply(seed, nonce, 1, inner)
	if err != nil {
		t.Fatal(err)
	}
	slot3, err := Apply(seed, nonce, 3, inner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Rewrap(seed, nonce, 1, 3, slot1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, slot3) || bytes.Equal(slot1, slot3) {
		t.Fatal("rewrap did not produce slot 3's bytes")
	}
}

func TestApply_refusesAShortSeedOrNonce(t *testing.T) {
	if _, err := Apply(make([]byte, 31), make([]byte, 32), 0, nil); !errors.Is(err, ErrSeed) {
		t.Fatalf("short seed: %v", err)
	}
	if _, err := Apply(make([]byte, 32), make([]byte, 31), 0, nil); err == nil {
		t.Fatal("short nonce accepted")
	}
}
