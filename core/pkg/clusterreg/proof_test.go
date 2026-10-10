package clusterreg

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func sampleProofs() Proofs {
	return Proofs{
		Signer: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID: "node-a",
		Proofs: []Proof{{
			DealID:    7,
			Slot:      2,
			LeafIndex: 3,
			Leaf:      bytes.Repeat([]byte{0xab}, 1024),
			Siblings:  [][]byte{bytes.Repeat([]byte{0xcd}, 32)},
		}},
	}
}

func TestEncodeProofs_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611aab08080710021803228008abababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab2a20cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	if hex.EncodeToString(EncodeProofs(sampleProofs())) != want {
		t.Fatalf("proofs %x", EncodeProofs(sampleProofs()))
	}
	zero := sampleProofs()
	zero.Proofs[0].Slot = 0
	zero.Proofs[0].LeafIndex = 0
	body := encodeReplicaProof(zero.Proofs[0])
	if len(body) < 3 || body[0] != 0x08 || body[1] != 0x07 || body[2] != 0x22 {
		t.Fatalf("zero slot and leaf index must be omitted, got %x", body[:8])
	}
}

func TestParseProofs_roundTripAndRejectsBadShape(t *testing.T) {
	p := sampleProofs().Proofs[0]
	raw, err := json.Marshal([]proofJSON{{
		DealID: p.DealID, Slot: p.Slot, LeafIndex: p.LeafIndex,
		Leaf: hex.EncodeToString(p.Leaf), Siblings: []string{hex.EncodeToString(p.Siblings[0])},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseProofs(raw)
	if err != nil {
		t.Fatal(err)
	}
	msg := sampleProofs()
	msg.Proofs = got
	if hex.EncodeToString(EncodeProofs(msg)) != hex.EncodeToString(EncodeProofs(sampleProofs())) {
		t.Fatal("json drifted from the sample")
	}
	if err := ValidateProofs(sampleProofs()); err != nil {
		t.Fatal(err)
	}
	bad := sampleProofs()
	bad.Proofs[0].Leaf = bad.Proofs[0].Leaf[:10]
	if err := ValidateProofs(bad); err == nil {
		t.Fatal("short leaf")
	}
	bad = sampleProofs()
	bad.Proofs[0].Siblings[0] = bad.Proofs[0].Siblings[0][:8]
	if err := ValidateProofs(bad); err == nil {
		t.Fatal("short sibling")
	}
	bad = sampleProofs()
	bad.Proofs = nil
	if err := ValidateProofs(bad); err == nil {
		t.Fatal("no proofs")
	}
	if _, err := ParseProofs([]byte(`{"deal_id":1}`)); err == nil {
		t.Fatal("object was accepted")
	}
}
