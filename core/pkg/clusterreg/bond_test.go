package clusterreg

import (
	"encoding/hex"
	"testing"
)

func TestEncodeBond_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611802220431303030"
	got := EncodeBond(Bond{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a",
		Role:     RoleStorage,
		Amount:   "1000",
	})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestValidateBond_rejectsZeroAndTheWrongRole(t *testing.T) {
	b := Bond{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a", Role: RoleStorage, Amount: "1000",
	}
	if err := ValidateBond(b); err != nil {
		t.Fatal(err)
	}
	b.Amount = "0"
	if err := ValidateBond(b); err == nil {
		t.Fatal("zero amount was accepted")
	}
	b.Amount = "0100"
	if err := ValidateBond(b); err == nil {
		t.Fatal("a leading zero was accepted")
	}
	b.Amount = "1000"
	b.Role = 0
	if err := ValidateBond(b); err == nil {
		t.Fatal("role 0 was accepted")
	}
}
