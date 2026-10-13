package namespacecmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/storagecmd"
	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

const (
	dealTestNonce  = "33333333333333333333333333333333" + "33333333333333333333333333333333"
	dealTestRPC    = "http://127.0.0.1:1"
	dealTestDealID = 7
)

// dealFixture is a restore fixture whose sealed backup was also sealed into a
// storage deal's slots. fetchDeal is replaced by a reader that opens those
// slot files the way storageclient.Get opens what a provider serves, so the
// slot layer, the keys and the backup's own seal are all the real code.
type dealFixture struct {
	*restoreFixture
	gw       gatewayTarget
	slotsDir string
	keys     storagecmd.Keys
	fetched  int
}

func newDealFixture(t *testing.T) *dealFixture {
	t.Helper()
	f, gw := newRestoreFixture(t)
	d := &dealFixture{restoreFixture: f, gw: gw, slotsDir: filepath.Join(f.dir, "slots")}
	d.keys = storagecmd.Keys{
		StorageKeyFile: f.write(t, "storage-key", []byte(strings.Repeat("11", storagefile.StorageKeyLen)+"\n")),
		RepairSeedFile: f.write(t, "repair-seed", []byte(strings.Repeat("22", 32)+"\n")),
	}
	blob, err := os.ReadFile(f.opts.inPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = sealForDeal(&out, blob, dealSealFlags{dir: d.slotsDir, nonce: dealTestNonce, replicas: 2, keys: d.keys})
	if err != nil {
		t.Fatal(err)
	}
	f.opts.inPath = ""
	f.opts.deal = dealFetchFlags{dealID: dealTestDealID, rpc: dealTestRPC, keys: d.keys}
	orig := fetchDeal
	fetchDeal = d.readSlot
	t.Cleanup(func() { fetchDeal = orig })
	return d
}

// readSlot is storageclient.Get against the slot files on disk.
func (d *dealFixture) readSlot(_ context.Context, rpc string, dealID uint64, keys storagecmd.Keys) ([]byte, error) {
	d.fetched++
	if rpc != dealTestRPC || dealID != dealTestDealID {
		return nil, os.ErrNotExist
	}
	storageKey := readHexFile(keys.StorageKeyFile)
	repair := readHexFile(keys.RepairSeedFile)
	nonce, _ := hex.DecodeString(dealTestNonce)
	slot, err := os.ReadFile(filepath.Join(d.slotsDir, "slot-0"))
	if err != nil {
		return nil, err
	}
	return storagefile.Open(storageKey, repair, nonce, 0, slot)
}

func readHexFile(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b, _ := hex.DecodeString(strings.TrimSpace(string(raw)))
	return b
}

func TestRunRestore_fromDeal_round_trip(t *testing.T) {
	d := newDealFixture(t)
	var out bytes.Buffer
	if err := runRestore(context.Background(), &out, d.gw, d.opts); err != nil {
		t.Fatalf("restore from the deal: %v", err)
	}
	if d.fetched != 1 || d.calls != 1 {
		t.Fatalf("fetched %d times, gateway called %d times; want one each", d.fetched, d.calls)
	}
	if d.got.Namespace != "myapp" || !bytes.Equal(d.got.RQLite, restoreTestDB) {
		t.Fatalf("the gateway got %q / %q, want the backup's namespace and database", d.got.Namespace, d.got.RQLite)
	}
}

func TestSealForDeal_writesOneSlotPerReplicaAndTheDealCommand(t *testing.T) {
	d := newDealFixture(t)
	blob := []byte("sealed backup bytes")
	var out bytes.Buffer
	dir := filepath.Join(d.dir, "again")
	if err := sealForDeal(&out, blob, dealSealFlags{dir: dir, nonce: dealTestNonce, replicas: 3, keys: d.keys}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(filepath.Join(dir, "slot-"+string(rune('0'+i)))); err != nil {
			t.Errorf("slot %d not written: %v", i, err)
		}
	}
	text := out.String()
	if strings.Count(text, "--piece ") != 3 || !strings.Contains(text, "--nonce "+dealTestNonce) || !strings.Contains(text, "--replicas 3") {
		t.Fatalf("the printed command does not carry the nonce, replica count and three pieces:\n%s", text)
	}
}

func TestRunRestore_fromDeal_wrong_storage_key_sends_nothing(t *testing.T) {
	d := newDealFixture(t)
	d.opts.deal.keys.StorageKeyFile = d.write(t, "other-key", []byte(strings.Repeat("99", storagefile.StorageKeyLen)+"\n"))
	err := runRestore(context.Background(), &bytes.Buffer{}, d.gw, d.opts)
	if err == nil || !strings.Contains(err.Error(), "deal 7") {
		t.Fatalf("err = %v, want the deal read to fail", err)
	}
	if d.calls != 0 {
		t.Fatalf("the gateway was called %d times after the deal would not open", d.calls)
	}
}

func TestRunRestore_fromDeal_wrong_backup_key_sends_nothing(t *testing.T) {
	d := newDealFixture(t)
	d.opts.keyFile = d.write(t, "wrong-owner", []byte(strings.Repeat("ab", 32)+"\n"))
	if err := runRestore(context.Background(), &bytes.Buffer{}, d.gw, d.opts); err == nil {
		t.Fatal("a deal opened with the wrong backup key restored")
	}
	if d.calls != 0 {
		t.Fatalf("the gateway was called %d times", d.calls)
	}
}

func TestRunRestore_fromDeal_truncated_slot_sends_nothing(t *testing.T) {
	d := newDealFixture(t)
	slot := filepath.Join(d.slotsDir, "slot-0")
	raw, err := os.ReadFile(slot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slot, raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRestore(context.Background(), &bytes.Buffer{}, d.gw, d.opts); err == nil {
		t.Fatal("a truncated slot restored")
	}
	if d.calls != 0 {
		t.Fatalf("the gateway was called %d times", d.calls)
	}
}

func TestRunRestore_fromDeal_truncated_backup_inside_a_valid_deal_sends_nothing(t *testing.T) {
	d := newDealFixture(t)
	short := []byte("ORBK-truncated")
	var out bytes.Buffer
	err := sealForDeal(&out, short, dealSealFlags{dir: d.slotsDir, nonce: dealTestNonce, replicas: 2, keys: d.keys})
	if err != nil {
		t.Fatal(err)
	}
	if err := runRestore(context.Background(), &bytes.Buffer{}, d.gw, d.opts); err == nil {
		t.Fatal("a backup cut short inside the deal restored")
	}
	if d.calls != 0 {
		t.Fatalf("the gateway was called %d times", d.calls)
	}
}

func TestRestoreBlob_needsExactlyOneSource(t *testing.T) {
	ctx := context.Background()
	deal := dealFetchFlags{dealID: 1, rpc: dealTestRPC}
	if _, err := restoreBlob(ctx, "x.orbk", deal); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("--in with --from-deal: %v", err)
	}
	if _, err := restoreBlob(ctx, "", dealFetchFlags{}); err == nil || !strings.Contains(err.Error(), "--in") {
		t.Errorf("neither source: %v", err)
	}
	if _, err := restoreBlob(ctx, "", dealFetchFlags{dealID: 1}); err == nil || !strings.Contains(err.Error(), "--rpc") {
		t.Errorf("--from-deal without --rpc: %v", err)
	}
}

func TestDealSealFlags_validateWantsTheKeysBeforeABackupIsTaken(t *testing.T) {
	if err := (dealSealFlags{}).validate(); err != nil {
		t.Errorf("no --deal-dir needs nothing: %v", err)
	}
	if err := (dealSealFlags{dir: "d", nonce: dealTestNonce}).validate(); err == nil {
		t.Error("--deal-dir without key files was accepted")
	}
}

func TestSealForDeal_refusesABadNonceAndAWrongReplicaCount(t *testing.T) {
	d := newDealFixture(t)
	for _, f := range []dealSealFlags{
		{dir: filepath.Join(d.dir, "a"), nonce: "zz", replicas: 2, keys: d.keys},
		{dir: filepath.Join(d.dir, "b"), nonce: dealTestNonce, replicas: 0, keys: d.keys},
	} {
		if err := sealForDeal(&bytes.Buffer{}, []byte("x"), f); err == nil {
			t.Errorf("sealForDeal accepted %+v", f)
		}
		if _, err := os.Stat(f.dir); err == nil {
			t.Errorf("%s was created for a refused seal", f.dir)
		}
	}
}
