package types

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
)

// Wire hexes are MsgCreateDeal, MsgExtendDeal, MsgAcceptDeal, and
// MsgDeclineDeal for the bodies below. core's clusterreg encoders must
// produce the same bytes.
const (
	createDealWireHex  = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d6131717971737a716770717971737a716770717971737a716770717971737a7167703663737a61651801222011111111111111111111111111111111111111111111111111111111111111112a087265706169722d3130033a0431303030401e4a290a200101010101010101010101010101010101010101010101010101010101010101100118012080084a290a200202020202020202020202020202020202020202020202020202020202020202100218022080104a290a20030303030303030303030303030303030303030303030303030303030303030310021802208108"
	publicPinWireHex   = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307318022220222222222222222222222222222222222222222222222222222222222222222230033a043130303040014a290a20040404040404040404040404040404040404040404040404040404040404040410011801208008"
	extendDealWireHex  = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307310071809"
	acceptDealWireHex  = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611807"
	declineDealWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61180720022a0466756c6c"
)

func TestStorageDealWire(t *testing.T) {
	root := func(b byte) []byte { return bytes.Repeat([]byte{b}, RootLen) }
	private := &MsgCreateDeal{
		Signer:         "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		Granter:        "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae",
		Class:          DealClass_DEAL_CLASS_PRIVATE,
		DealNonce:      bytes.Repeat([]byte{0x11}, NonceLen),
		RepairDelegate: "repair-1",
		Replicas:       3,
		PricePerEpoch:  math.NewInt(1000),
		DurationEpochs: 30,
		Pieces: []PieceCommitment{
			{Root: root(0x01), RealLeafCount: 1, PaddedLeafCount: 1, PieceBytes: 1024},
			{Root: root(0x02), RealLeafCount: 2, PaddedLeafCount: 2, PieceBytes: 2048},
			{Root: root(0x03), RealLeafCount: 2, PaddedLeafCount: 2, PieceBytes: 1025},
		},
	}
	public := &MsgCreateDeal{
		Signer:         private.Signer,
		Class:          DealClass_DEAL_CLASS_PUBLIC_PIN,
		DealNonce:      bytes.Repeat([]byte{0x22}, NonceLen),
		Replicas:       3,
		PricePerEpoch:  math.NewInt(1000),
		DurationEpochs: 1,
		Pieces: []PieceCommitment{
			{Root: root(0x04), RealLeafCount: 1, PaddedLeafCount: 1, PieceBytes: 1024},
		},
	}
	extend := &MsgExtendDeal{Signer: private.Signer, DealId: 7, ExtraEpochs: 9}
	accept := &MsgAcceptDeal{Signer: private.Signer, NodeId: "node-a", DealId: 7}
	decline := &MsgDeclineDeal{Signer: private.Signer, NodeId: "node-a", DealId: 7, Slot: 2, Reason: "full"}
	check := func(name, want string, msg interface{ Marshal() ([]byte, error) }) {
		t.Helper()
		got, err := msg.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != want {
			t.Errorf("%s %x", name, got)
		}
	}
	check("create", createDealWireHex, private)
	check("public", publicPinWireHex, public)
	check("extend", extendDealWireHex, extend)
	check("accept", acceptDealWireHex, accept)
	check("decline", declineDealWireHex, decline)
}
