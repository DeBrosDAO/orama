package repair

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/storagekey"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

type stubChain struct {
	deal  types.Deal
	slots []types.Slot
}

func (c stubChain) Deal(context.Context, uint64) (types.Deal, error) { return c.deal, nil }
func (c stubChain) Slot(_ context.Context, _ uint64, i uint32) (types.Slot, error) {
	return c.slots[i], nil
}
func (c stubChain) ProviderURL(_ context.Context, id string) (string, error) {
	return "http://" + id, nil
}

type memNet struct {
	pieces   map[string][]byte
	uploaded map[string][]byte
}

func (n *memNet) Fetch(_ context.Context, base string, _ []byte) ([]byte, error) {
	b, ok := n.pieces[base]
	if !ok {
		return nil, errors.New("down")
	}
	return b, nil
}

func (n *memNet) Upload(_ context.Context, base string, _, body []byte) error {
	n.uploaded[base] = body
	return nil
}

func fixture(t *testing.T, seed []byte) (stubChain, *memNet) {
	t.Helper()
	nonce := bytes.Repeat([]byte{8}, 32)
	inner := bytes.Repeat([]byte{1, 2, 3}, 3000)
	net := &memNet{pieces: map[string][]byte{}, uploaded: map[string][]byte{}}
	c := stubChain{deal: types.Deal{Id: 1, DealNonce: nonce, Replicas: 3, RepairDelegate: "delegate"}}
	for j := uint32(0); j < 3; j++ {
		b, err := storagekey.Apply(seed, nonce, j, inner)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := piece.Commit(b)
		slot := types.Slot{DealId: 1, Index: j, NodeId: "n" + string(rune('0'+j)), Operator: "op", PieceRoot: root.Root,
			Status: types.SlotStatus_SLOT_STATUS_ACTIVE, Accepted: true}
		if j == 2 {
			slot.Status, slot.Accepted = types.SlotStatus_SLOT_STATUS_ASSIGNED, false
		} else {
			net.pieces["http://"+slot.NodeId] = b
		}
		c.slots = append(c.slots, slot)
	}
	return c, net
}

func TestRepairDeal_rebuildsTheMissingSlotFromASurvivor(t *testing.T) {
	seed := bytes.Repeat([]byte{5}, 32)
	c, net := fixture(t, seed)
	delete(net.pieces, "http://n0")
	d, err := New(c, net, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	done, err := d.RepairDeal(context.Background(), 1, seed)
	if err != nil || len(done) != 1 || done[0].From != 1 {
		t.Fatalf("done %+v err %v", done, err)
	}
	got, _ := piece.Commit(net.uploaded["http://n2"])
	if !bytes.Equal(got.Root, c.slots[2].PieceRoot) {
		t.Fatal("uploaded bytes do not match slot 2")
	}
}

func TestRepairDeal_refusesAWrongSeedAnotherDelegateAndItsOwnSlot(t *testing.T) {
	seed := bytes.Repeat([]byte{5}, 32)
	c, net := fixture(t, seed)
	d, _ := New(c, net, "delegate")
	if _, err := d.RepairDeal(context.Background(), 1, bytes.Repeat([]byte{6}, 32)); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("wrong seed: %v", err)
	}
	if len(net.uploaded) != 0 {
		t.Fatal("a wrong seed uploaded bytes")
	}
	other, _ := New(c, net, "someone-else")
	if _, err := other.RepairDeal(context.Background(), 1, seed); !errors.Is(err, ErrNotDelegate) {
		t.Fatalf("other delegate: %v", err)
	}
	c.slots[1].Operator = "delegate"
	own, _ := New(c, net, "delegate")
	if _, err := own.RepairDeal(context.Background(), 1, seed); !errors.Is(err, ErrOwnSlot) {
		t.Fatalf("own slot: %v", err)
	}
}

func TestRepairDeal_leavesANewDealAlone(t *testing.T) {
	seed := bytes.Repeat([]byte{5}, 32)
	c, net := fixture(t, seed)
	for i := range c.slots {
		c.slots[i].Status, c.slots[i].Accepted = types.SlotStatus_SLOT_STATUS_ASSIGNED, false
	}
	d, _ := New(c, net, "delegate")
	done, err := d.RepairDeal(context.Background(), 1, seed)
	if err != nil || len(done) != 0 || len(net.uploaded) != 0 {
		t.Fatalf("done %v err %v", done, err)
	}
}

func TestLoadSeeds_refusesReadableMismatchedAndShortSeeds(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadSeeds(filepath.Join(dir, "absent"))
	if err != nil || len(got) != 0 {
		t.Fatal("an absent directory is not empty")
	}
	good := `{"deal_id":4,"repair_seed":"` + string(bytes.Repeat([]byte("ab"), 32)) + `"}`
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("4.json", good, 0o600)
	seeds, err := LoadSeeds(dir)
	if err != nil || len(seeds[4]) != 32 {
		t.Fatalf("good seed: %v", err)
	}
	for name, body := range map[string]string{
		"5.json": good,
		"6.json": `{"deal_id":6,"repair_seed":"abcd"}`,
		"7.json": `{`,
	} {
		sub := t.TempDir()
		if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSeeds(sub); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "4.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSeeds(dir); err == nil {
		t.Fatal("a readable seed was accepted")
	}
}

func TestPublicHTTPClient_refusesALoopbackEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, err := HTTP{Client: PublicHTTPClient(time.Second)}.Fetch(context.Background(), srv.URL, make([]byte, 32))
	if !errors.Is(err, ErrNotPublic) {
		t.Fatalf("got %v, want ErrNotPublic", err)
	}
}
