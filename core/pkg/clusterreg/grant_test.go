package clusterreg

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func sampleGrant() Grant {
	grantee, err := bech32Encode(accountHRP, bytes.Repeat([]byte{0x01}, 20))
	if err != nil {
		panic(err)
	}
	return Grant{
		Signer:        "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		Grantee:       grantee,
		SpendLimit:    "1000",
		PeriodEpochs:  7,
		MaxPieceBytes: 4096,
		MaxDuration:   30,
		Replicas:      3,
		ExpiryEpoch:   100,
	}
}

func TestEncodeGrant_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d6131717971737a716770717971737a716770717971737a716770717971737a7167703663737a61651a04313030302007288020301e38034064"
	if hex.EncodeToString(EncodeGrant(sampleGrant())) != want {
		t.Fatalf("grant %x", EncodeGrant(sampleGrant()))
	}
	const revoke = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d6131717971737a716770717971737a716770717971737a716770717971737a7167703663737a6165"
	if hex.EncodeToString(EncodeRevokeGrant(sampleGrant().Signer, sampleGrant().Grantee)) != revoke {
		t.Fatalf("revoke %x", EncodeRevokeGrant(sampleGrant().Signer, sampleGrant().Grantee))
	}
}

func TestValidateGrant_refusesSameAccountAndBadLimits(t *testing.T) {
	g := sampleGrant()
	g.Grantee = g.Signer
	if err := ValidateGrant(g); err == nil {
		t.Fatal("same account")
	}
	g = sampleGrant()
	g.SpendLimit = "0"
	if err := ValidateGrant(g); err == nil {
		t.Fatal("zero spend")
	}
	g = sampleGrant()
	g.Replicas = 2
	if err := ValidateGrant(g); err == nil {
		t.Fatal("replicas")
	}
	if err := ValidateGrant(sampleGrant()); err != nil {
		t.Fatal(err)
	}
}
