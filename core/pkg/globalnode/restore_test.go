package globalnode

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

// resealed is a restored-backup bundle for dst, from src's key.
func resealed(t *testing.T, src, dst Host) []byte {
	t.Helper()
	opPub, opPriv, _ := box.GenerateKey(rand.Reader)
	backup, err := src.ExportKey(opPub)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := dst.PrepareMigration()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Reseal(opPriv, recipient, backup)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestReseal_restoreStartsAtTheGivenFloor(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	bundle := resealed(t, src, dst)
	floor, err := RestoreFloor(5000)
	if err != nil {
		t.Fatal(err)
	}
	res, err := dst.ImportMigration(bundle, &floor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Floor == nil || *res.Floor != (SignState{5001, 0, 0}) {
		t.Fatalf("floor = %v, want the next height, 5001, before any step", res.Floor)
	}
	if !bytes.Equal(read(t, dst.KeyPath), read(t, src.KeyPath)) {
		t.Fatal("the restored key is not the backed-up key")
	}
	if got, _ := ParseSignState(read(t, dst.StatePath)); got != (SignState{5001, 0, 0}) {
		t.Fatalf("restored state = %v, want the floor", got)
	}
	for _, below := range [][]byte{emptySignState, stateJSON("5000", 9, 3)} {
		write(t, dst.StatePath, below)
		if err := dst.CheckSignFloor(); err == nil {
			t.Fatalf("state %s was accepted at or below the latest committed height", below)
		}
	}
}

func TestReseal_restoreWithoutAFloorRefused(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	before := read(t, dst.KeyPath)
	if _, err := dst.ImportMigration(resealed(t, src, dst), nil); err == nil || !strings.Contains(err.Error(), "current height") {
		t.Fatalf("err = %v, want the restore floor demanded", err)
	}
	if !bytes.Equal(read(t, dst.KeyPath), before) {
		t.Fatal("the key changed after a refused restore")
	}
	if _, err := RestoreFloor(0); err == nil {
		t.Fatal("a zero floor height was accepted")
	}
}

func TestImportMigration_migrationBundleRefusesARestoreFloor(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	recipient, _ := dst.PrepareMigration()
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	floor, _ := RestoreFloor(10)
	if _, err := dst.ImportMigration(bundle, &floor); err == nil {
		t.Fatal("a migration bundle took an operator floor over its own state")
	}
}

func TestImportMigration_neverLowersARecordedFloor(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	write(t, dst.floorPath(), stateJSON("2000", 0, 3))
	write(t, dst.StatePath, stateJSON("2000", 0, 3))
	write(t, src.StatePath, stateJSON("1200", 0, 3))
	before := read(t, dst.KeyPath)
	recipient, _ := dst.PrepareMigration()
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportMigration(bundle, nil); err == nil || !strings.Contains(err.Error(), "never lowered") {
		t.Fatalf("err = %v, want the lower floor refused", err)
	}
	if got, _ := ParseSignState(read(t, dst.floorPath())); got.Height != 2000 {
		t.Fatalf("the floor was lowered to %v", got)
	}
	if !bytes.Equal(read(t, dst.KeyPath), before) {
		t.Fatal("the key was installed from a bundle behind the floor")
	}
}
