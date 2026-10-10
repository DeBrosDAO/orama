package types

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
)

func TestMsgGrantDealAuthorization_wire(t *testing.T) {
	msg := &MsgGrantDealAuthorization{
		Signer:            "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		Grantee:           "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae",
		SpendLimit:        math.NewInt(1000),
		PeriodEpochs:      7,
		MaxPieceBytes:     4096,
		MaxDurationEpochs: 30,
		Replicas:          3,
		ExpiryEpoch:       100,
	}
	got, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	const grantHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d6131717971737a716770717971737a716770717971737a716770717971737a7167703663737a61651a04313030302007288020301e38034064"
	if hex.EncodeToString(got) != grantHex {
		t.Fatalf("grant %x", got)
	}
	revoke := &MsgRevokeDealAuthorization{Signer: msg.Signer, Grantee: msg.Grantee}
	got, err = revoke.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	const revokeHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d6131717971737a716770717971737a716770717971737a716770717971737a7167703663737a6165"
	if hex.EncodeToString(got) != revokeHex {
		t.Fatalf("revoke %x", got)
	}
}
