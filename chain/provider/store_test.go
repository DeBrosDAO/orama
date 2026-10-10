package provider

import (
	"bytes"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func TestIngestDeclinesABadRootDenylistAndFullDisk(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte{7}, 100)
	good, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, []string{"# comment", "banned"}, func() (uint64, error) { return 1000, nil })
	if err != nil {
		t.Fatal(err)
	}
	bad, err := s.Ingest("piece-a", data, []byte("nope-nope-nope-nope-nope-nope-no"))
	if err != nil || bad.Accept || bad.Reason != ReasonRoot {
		t.Fatalf("root %+v %v", bad, err)
	}
	if s.Has("piece-a") {
		t.Fatal("mismatch was stored")
	}
	denied, err := s.Ingest("banned", data, good.Root)
	if err != nil || denied.Reason != ReasonDenylist {
		t.Fatalf("deny %+v %v", denied, err)
	}
	full, err := Open(dir, nil, func() (uint64, error) { return 10, nil })
	if err != nil {
		t.Fatal(err)
	}
	disk, err := full.Ingest("piece-a", data, good.Root)
	if err != nil || disk.Reason != ReasonDisk {
		t.Fatalf("disk %+v %v", disk, err)
	}
}

func TestIngestRestartAndProofs(t *testing.T) {
	dir := t.TempDir()
	// Three real leaves: indexes 0, 1, 2. The padded tree has 4 leaves.
	data := bytes.Repeat([]byte{9}, piece.LeafSize*3-10)
	c, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	if c.RealLeafCount != 3 || c.PaddedLeafCount != 4 {
		t.Fatalf("leaves real=%d padded=%d", c.RealLeafCount, c.PaddedLeafCount)
	}
	s, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Ingest("piece-a", data, c.Root)
	if err != nil || !got.Accept {
		t.Fatalf("ingest %+v %v", got, err)
	}
	other := []byte("other")
	otherCommit, err := piece.Commit(other)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Ingest("piece-a.json", other, otherCommit.Root)
	if err != nil || !second.Accept {
		t.Fatalf("second ingest %+v %v", second, err)
	}
	root, err := s.ReadRoot("piece-a")
	if err != nil || !bytes.Equal(root, c.Root) {
		t.Fatalf("metadata collided: %x %v", root, err)
	}
	again, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []uint64{0, 2} {
		committed, proof, err := again.Prove("piece-a", index)
		if err != nil {
			t.Fatal(err)
		}
		if err := piece.VerifyChallenge(committed, proof); err != nil {
			t.Fatal(err)
		}
	}
	pad, err := piece.Prove(data, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := piece.VerifyChallenge(c, pad); err == nil {
		t.Fatal("padding leaf was accepted as a challenge")
	}
	missing := again.Absent([]string{"piece-a", "piece-b", "../outside"})
	if len(missing) != 2 || missing[0] != "piece-b" || missing[1] != "../outside" {
		t.Fatalf("absent %v", missing)
	}
	if _, _, err := again.Prove("../outside", 0); err == nil {
		t.Fatal("path escape was proved")
	}
}
