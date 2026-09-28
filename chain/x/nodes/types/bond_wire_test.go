package types

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
)

// bondWireHex is the protobuf body of MsgBondNode and MsgUnbondNode for the
// fields below. core/pkg/clusterreg.EncodeBond must produce the same bytes.
const bondWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611802220431303030"

func TestMsgBondAndUnbond_wireMatchesCoreEncoder(t *testing.T) {
	amount := math.NewInt(1000)
	bond := &MsgBondNode{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:   "node-a",
		Role:     RoleStorage,
		Amount:   amount,
	}
	got, err := bond.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	unbond := &MsgUnbondNode{
		Operator: bond.Operator,
		NodeId:   bond.NodeId,
		Role:     bond.Role,
		Amount:   amount,
	}
	other, err := unbond.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(other) {
		t.Fatalf("bond %x unbond %x", got, other)
	}
	if hex.EncodeToString(got) != bondWireHex {
		t.Fatalf("wire %x", got)
	}
}
