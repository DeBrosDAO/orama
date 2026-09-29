package globalnode

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestExportMigration_chainStartedDuringExportAborts(t *testing.T) {
	src := newHost(t)
	pub, _, _ := box.GenerateKey(rand.Reader)
	var sentinelSeen error
	started := func() error {
		sentinelSeen = src.CheckSignFloor()
		return errors.New("the chain is active")
	}
	if _, _, err := src.ExportMigration(pub, started); err == nil {
		t.Fatal("the export went on after the chain started")
	}
	if sentinelSeen == nil || !strings.Contains(sentinelSeen.Error(), "in progress") {
		t.Fatalf("a start during the export was not refused: %v", sentinelSeen)
	}
	if _, err := os.Stat(src.KeyPath); err != nil {
		t.Fatal("an aborted export moved the key")
	}
	if err := src.CheckSignFloor(); err != nil {
		t.Fatalf("an aborted export left the chain unable to start: %v", err)
	}
}

func TestCheckSignFloor_refusesAStateRootOthersCanWrite(t *testing.T) {
	h := newHost(t)
	write(t, h.floorPath(), stateJSON("1", 0, 3))
	if err := os.Chmod(h.StateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(h.StateDir, 0o700) })
	if err := h.CheckSignFloor(); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("err = %v, want a refusal of the writable state root", err)
	}
	if _, err := h.PrepareMigration(); err == nil {
		t.Fatal("a migration key was written into a writable state root")
	}
	if err := os.Chmod(h.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h.StateOwner = os.Getuid() + 1
	if err := h.CheckSignFloor(); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("err = %v, want a refusal of a foreign-owned state root", err)
	}
}

func TestMigratedAway_onlyWithAFloorAndNoKey(t *testing.T) {
	h := newHost(t)
	if away, err := h.MigratedAway(); err != nil || away {
		t.Fatalf("fresh host: %v %v", away, err)
	}
	pub, _, _ := box.GenerateKey(rand.Reader)
	if _, _, err := h.ExportMigration(pub, stopped); err != nil {
		t.Fatal(err)
	}
	if away, err := h.MigratedAway(); err != nil || !away {
		t.Fatalf("after export: %v %v", away, err)
	}
}

func TestExportMigration_oldHostCannotStartAgain(t *testing.T) {
	src := newHost(t)
	key := read(t, src.KeyPath)
	state := stateJSON("900", 0, 3)
	write(t, src.StatePath, state)
	pub, _, _ := box.GenerateKey(rand.Reader)
	_, out, err := src.ExportMigration(pub, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != (SignState{900, 0, 3}) {
		t.Fatalf("exported state = %v", out.State)
	}
	if err := src.CheckSignFloor(); err == nil || !strings.Contains(err.Error(), "validator key") {
		t.Fatalf("err = %v, want a refusal: the key left this host", err)
	}
	// What oramad's LoadOrGenFilePV would write on a start without the key.
	write(t, src.KeyPath, validatorKeyJSON(t))
	write(t, src.StatePath, emptySignState)
	if err := src.CheckSignFloor(); err == nil {
		t.Fatal("a fresh key with a zero state started below the floor")
	}
	// Abandoning: put back the key and the state copy.
	write(t, src.KeyPath, read(t, out.KeyCopy))
	write(t, src.StatePath, read(t, out.StateCopy))
	if !bytes.Equal(read(t, src.KeyPath), key) {
		t.Fatal("the key copy is not the key")
	}
	if err := src.CheckSignFloor(); err != nil {
		t.Fatalf("the restored key and state are refused: %v", err)
	}
}
