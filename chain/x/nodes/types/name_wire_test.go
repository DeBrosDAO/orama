package types

import (
	"encoding/hex"
	"testing"
)

// claimNameWireHex is the protobuf body of MsgClaimNodeName for the fields below, and
// releaseNameWireHex that of MsgReleaseNodeName. core/pkg/clusterreg.EncodeClaimNodeName and
// EncodeReleaseNodeName must produce the same bytes.
const (
	claimNameWireHex   = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a09616c7068612d6f6e65"
	releaseNameWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61"
)

func TestMsgClaimNodeName_wireMatchesCoreEncoder(t *testing.T) {
	got, err := (&MsgClaimNodeName{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:   "node-a",
		Name:     "alpha-one",
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != claimNameWireHex {
		t.Fatalf("wire %x", got)
	}
}

func TestMsgReleaseNodeName_wireMatchesCoreEncoder(t *testing.T) {
	got, err := (&MsgReleaseNodeName{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:   "node-a",
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != releaseNameWireHex {
		t.Fatalf("wire %x", got)
	}
}
