package storage

import (
	"context"
	"errors"
	"testing"
)

func TestUnpinIfLastPinner_sharedOwnershipSkipsUnpin(t *testing.T) {
	ipfs := &mockIPFSClient{}
	db := &mockStorageDB{otherPinCount: 1}
	if err := UnpinIfLastPinner(context.Background(), db, ipfs, "QmX", "ns-a"); err != nil {
		t.Fatal(err)
	}
	if ipfs.unpinCalls != 0 {
		t.Fatalf("must not unpin a CID another namespace still pins, unpins=%d", ipfs.unpinCalls)
	}
}

func TestUnpinIfLastPinner_lastPinnerUnpins(t *testing.T) {
	ipfs := &mockIPFSClient{}
	db := &mockStorageDB{otherPinCount: 0}
	if err := UnpinIfLastPinner(context.Background(), db, ipfs, "QmX", "ns-a"); err != nil {
		t.Fatal(err)
	}
	if ipfs.unpinCalls != 1 {
		t.Fatalf("last pinner must unpin, unpins=%d", ipfs.unpinCalls)
	}
}

func TestUnpinIfLastPinner_emptyCIDNoop(t *testing.T) {
	ipfs := &mockIPFSClient{}
	if err := UnpinIfLastPinner(context.Background(), &mockStorageDB{}, ipfs, "", "ns-a"); err != nil {
		t.Fatal(err)
	}
	if ipfs.unpinCalls != 0 {
		t.Fatal("empty cid must not unpin")
	}
}

func TestUnpinIfLastPinner_refcountErrorLeavesPin(t *testing.T) {
	ipfs := &mockIPFSClient{}
	db := &mockStorageDB{otherQueryErr: errors.New("db down")}
	if err := UnpinIfLastPinner(context.Background(), db, ipfs, "QmX", "ns-a"); err != nil {
		t.Fatal(err)
	}
	if ipfs.unpinCalls != 0 {
		t.Fatal("refcount error must leave the cluster pin")
	}
}
