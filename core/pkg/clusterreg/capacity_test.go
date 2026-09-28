package clusterreg

import (
	"encoding/hex"
	"testing"
)

func TestEncodeCapacity_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61188020"
	got := EncodeCapacity(Capacity{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a",
		Bytes:    4096,
	})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
	zero := EncodeCapacity(Capacity{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a",
	})
	if hex.EncodeToString(zero) != "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61" {
		t.Fatalf("zero %x", zero)
	}
}

func TestEncodeRetire_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61"
	got := EncodeRetire(Retire{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		ID:       "node-a",
	})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestValidateCapacity_allowsZero(t *testing.T) {
	c := Capacity{Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", NodeID: "node-a"}
	if err := ValidateCapacity(c); err != nil {
		t.Fatal(err)
	}
}
