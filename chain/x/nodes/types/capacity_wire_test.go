package types

import (
	"encoding/hex"
	"testing"
)

const (
	capacityWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61188020"
	capacityZeroHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61"
	retireWireHex   = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61"
)

func TestMsgDeclareCapacity_wireMatchesCoreEncoder(t *testing.T) {
	msg := &MsgDeclareCapacity{
		Operator:      "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:        "node-a",
		CapacityBytes: 4096,
	}
	got, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != capacityWireHex {
		t.Fatalf("wire %x", got)
	}
	zero := &MsgDeclareCapacity{Operator: msg.Operator, NodeId: msg.NodeId}
	got, err = zero.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != capacityZeroHex {
		t.Fatalf("zero %x", got)
	}
}

func TestMsgRetire_wireMatchesCoreEncoder(t *testing.T) {
	node := &MsgRetireNode{Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", NodeId: "node-a"}
	cluster := &MsgRetireCluster{Operator: node.Operator, ClusterId: node.NodeId}
	got, err := node.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	other, err := cluster.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(other) {
		t.Fatalf("node %x cluster %x", got, other)
	}
	if hex.EncodeToString(got) != retireWireHex {
		t.Fatalf("wire %x", got)
	}
}
