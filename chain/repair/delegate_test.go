package repair

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/storagekey"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

type stubChain struct {
	deal  types.Deal
	slots []types.Slot
	// heightErr makes Height fail, as a node that is busy or restarting does.
	heightErr error
	// order, when set, is told "height" each time Height is read.
	order *[]string
}

func (c stubChain) Deal(context.Context, uint64) (types.Deal, error) { return c.deal, nil }
func (c stubChain) Slot(_ context.Context, _ uint64, i uint32) (types.Slot, error) {
	return c.slots[i], nil
}
func (c stubChain) ProviderURL(_ context.Context, id string) (string, error) {
	return "http://" + id, nil
}

// stubHeight is the chain height every stubChain reports.
const stubHeight = 150

func (c stubChain) Height(context.Context) (int64, error) {
	if c.order != nil {
		*c.order = append(*c.order, "height")
	}
	if c.heightErr != nil {
		return 0, c.heightErr
	}
	return stubHeight, nil
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
	c.slots[2].AssignHeight = 100
	d, err := New(c, net, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	done, err := d.RepairDeal(context.Background(), 1, seed)
	if err != nil || len(done) != 1 || done[0].From != 1 {
		t.Fatalf("done %+v err %v", done, err)
	}
	if !done[0].BlocksKnown || done[0].BlocksSinceAssigned != stubHeight-100 {
		t.Fatalf("restore latency %d blocks, want %d", done[0].BlocksSinceAssigned, stubHeight-100)
	}
	got, _ := piece.Commit(net.uploaded["http://n2"])
	if !bytes.Equal(got.Root, c.slots[2].PieceRoot) {
		t.Fatal("uploaded bytes do not match slot 2")
	}
}

// The height only measures how long the restore took. A node that cannot say
// what it is must not undo, or refuse, the restore: the replica is uploaded,
// the failure is logged as an error, and the metric is left out.
func TestRepairDeal_aFailedHeightReadDoesNotFailTheRestore(t *testing.T) {
	seed := bytes.Repeat([]byte{5}, 32)
	c, net := fixture(t, seed)
	c.heightErr = errors.New("rpc: connection refused")
	d, err := New(c, net, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d.log = slog.New(slog.NewTextHandler(&logs, nil))

	done, err := d.RepairDeal(context.Background(), 1, seed)
	if err != nil || len(done) != 1 || done[0].Slot != 2 {
		t.Fatalf("done %+v err %v", done, err)
	}
	if done[0].BlocksKnown || done[0].BlocksSinceAssigned != 0 {
		t.Fatalf("a metric was reported without a height: %+v", done[0])
	}
	if _, ok := net.uploaded["http://n2"]; !ok {
		t.Fatal("the replica was not uploaded")
	}
	if out := logs.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "connection refused") {
		t.Fatalf("the failed read was not logged as an error: %q", out)
	}
}

// orderNet tells order about each upload.
type orderNet struct {
	*memNet
	order *[]string
}

func (n orderNet) Upload(ctx context.Context, base string, root, body []byte) error {
	*n.order = append(*n.order, "upload")
	return n.memNet.Upload(ctx, base, root, body)
}

// The height is read once the upload is done, so the latency includes it.
func TestRepairDeal_readsTheHeightAfterTheUpload(t *testing.T) {
	seed := bytes.Repeat([]byte{5}, 32)
	c, net := fixture(t, seed)
	var order []string
	c.order = &order
	d, err := New(c, orderNet{net, &order}, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RepairDeal(context.Background(), 1, seed); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"upload", "height"}) {
		t.Fatalf("order = %v, want the upload first", order)
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

func TestLoadSeeds_oneBadFileDoesNotHideTheOthers(t *testing.T) {
	dir := t.TempDir()
	good := `{"deal_id":4,"repair_seed":"` + string(bytes.Repeat([]byte("ab"), 32)) + `"}`
	if err := os.WriteFile(filepath.Join(dir, "4.json"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "9.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	seeds, err := LoadSeeds(dir)
	if err == nil {
		t.Fatal("the malformed seed file was not reported")
	}
	if len(seeds[4]) != 32 || len(seeds) != 1 {
		t.Fatalf("deal 4's seed must be served and deal 9 left out, got %d seeds", len(seeds))
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
