package globalnode

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

// stopped is a chain that stays stopped.
func stopped() error { return nil }

// migrate runs the source half on src and the target half on dst.
func migrate(t *testing.T, src, dst Host) ImportResult {
	t.Helper()
	recipient, err := dst.PrepareMigration()
	if err != nil {
		t.Fatal(err)
	}
	again, err := dst.PrepareMigration()
	if err != nil || *again != *recipient {
		t.Fatalf("a second prepare changed the recipient (%v)", err)
	}
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	res, err := dst.ImportMigration(bundle, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMigration_movesKeyAndStateAndRecordsTheFloor(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	key := read(t, src.KeyPath)
	write(t, src.StatePath, stateJSON("1200", 1, 3))
	res := migrate(t, src, dst)

	if _, err := os.Stat(src.KeyPath); !os.IsNotExist(err) {
		t.Fatal("the source still has the key in its chain home")
	}
	if !bytes.Equal(read(t, dst.KeyPath), key) {
		t.Fatal("the target does not have the source key")
	}
	if got, _ := ParseSignState(read(t, dst.StatePath)); got != (SignState{1200, 1, 3}) {
		t.Fatalf("target state = %v", got)
	}
	if res.Floor == nil || *res.Floor != (SignState{1200, 1, 3}) {
		t.Fatalf("floor = %v", res.Floor)
	}
	if res.Replaced == "" || !strings.HasPrefix(filepath.Base(res.Replaced), "validator-key-replaced-") {
		t.Fatalf("the target's own init key was not quarantined: %q", res.Replaced)
	}
	if _, err := os.Stat(dst.recipientPath()); !os.IsNotExist(err) {
		t.Fatal("the used migration key was left on the target")
	}
	if err := dst.CheckSignFloor(); err != nil {
		t.Fatalf("the migrated state fails its own floor: %v", err)
	}
}

func TestCheckSignFloor_refusesATargetBehindTheSource(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	write(t, src.StatePath, stateJSON("1200", 0, 2))
	migrate(t, src, dst)
	write(t, dst.StatePath, stateJSON("1199", 5, 3))
	err := dst.CheckSignFloor()
	if err == nil || !strings.Contains(err.Error(), "double sign") {
		t.Fatalf("err = %v, want the double-sign refusal", err)
	}
	write(t, dst.StatePath, stateJSON("1200", 0, 1))
	if err := dst.CheckSignFloor(); err == nil {
		t.Fatal("a lower step at the same height was accepted")
	}
	if err := os.Remove(dst.StatePath); err != nil {
		t.Fatal(err)
	}
	if err := dst.CheckSignFloor(); err == nil {
		t.Fatal("a missing state was accepted with a floor recorded")
	}
}

func TestImportMigration_keepsATargetStateAlreadyAhead(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	write(t, src.StatePath, stateJSON("100", 0, 3))
	write(t, dst.StatePath, stateJSON("500", 0, 3))
	migrate(t, src, dst)
	if got, _ := ParseSignState(read(t, dst.StatePath)); got.Height != 500 {
		t.Fatalf("target state went back to %v", got)
	}
}

func TestImportMigration_refusesWithoutPrepare(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	pub, _, _ := box.GenerateKey(rand.Reader)
	bundle, _, err := src.ExportMigration(pub, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportMigration(bundle, nil); err == nil || !strings.Contains(err.Error(), "prepare") {
		t.Fatalf("err = %v, want a pointer to the prepare step", err)
	}
}

func TestImportMigration_refusesABundleForAnotherHost(t *testing.T) {
	src, dst, other := newHost(t), newHost(t), newHost(t)
	if _, err := dst.PrepareMigration(); err != nil {
		t.Fatal(err)
	}
	otherPub, err := other.PrepareMigration()
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := src.ExportMigration(otherPub, stopped)
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, dst.KeyPath)
	if _, err := dst.ImportMigration(bundle, nil); err == nil {
		t.Fatal("a bundle sealed to another host was imported")
	}
	if !bytes.Equal(read(t, dst.KeyPath), before) {
		t.Fatal("the target key changed after a refused import")
	}
}

func TestImportMigration_refusesAnUninitialisedHome(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	if err := os.RemoveAll(filepath.Dir(dst.KeyPath)); err != nil {
		t.Fatal(err)
	}
	recipient, err := dst.PrepareMigration()
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportMigration(bundle, nil); err == nil || !strings.Contains(err.Error(), "--init-chain") {
		t.Fatalf("err = %v, want a pointer to --init-chain", err)
	}
}

func TestImportMigration_sameKeyTwiceIsIdempotent(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	write(t, src.StatePath, stateJSON("7", 0, 3))
	key := read(t, src.KeyPath)
	recipient, _ := dst.PrepareMigration()
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportMigration(bundle, nil); err != nil {
		t.Fatal(err)
	}
	recipient2, _ := dst.PrepareMigration()
	bundle2, err := SealBundle(recipient2, key, stateJSON("7", 0, 3))
	if err != nil {
		t.Fatal(err)
	}
	res, err := dst.ImportMigration(bundle2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced != "" {
		t.Fatalf("the same key was quarantined as a different one: %s", res.Replaced)
	}
}

func TestImportMigration_stateWriteFailureInstallsNoKey(t *testing.T) {
	src, dst := newHost(t), newHost(t)
	write(t, src.StatePath, stateJSON("1200", 0, 3))
	before := read(t, dst.KeyPath)
	recipient, err := dst.PrepareMigration()
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := src.ExportMigration(recipient, stopped)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Dir(dst.StatePath)
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dataDir, 0o700) })
	if _, err := dst.ImportMigration(bundle, nil); err == nil {
		t.Fatal("the import succeeded without writing the state")
	}
	if !bytes.Equal(read(t, dst.KeyPath), before) {
		t.Fatal("the migrated key was installed although its state was not")
	}
	// Were the migrated key put in place by hand now, its state is behind.
	write(t, dst.KeyPath, read(t, filepath.Join(src.StateDir, keyCopyName(t, src))))
	if err := dst.CheckSignFloor(); err == nil {
		t.Fatal("the migrated key could start below its recorded floor")
	}
}

func TestCancelMigration_removesTheMigrationKey(t *testing.T) {
	h := newHost(t)
	if removed, err := h.CancelMigration(); err != nil || removed {
		t.Fatalf("cancel with nothing prepared: %v %v", removed, err)
	}
	if _, err := h.PrepareMigration(); err != nil {
		t.Fatal(err)
	}
	if removed, err := h.CancelMigration(); err != nil || !removed {
		t.Fatalf("cancel: %v %v", removed, err)
	}
	if _, err := os.Stat(h.recipientPath()); !os.IsNotExist(err) {
		t.Fatal("the migration key is still there")
	}
}

func TestKeepCopy_namesAreUniqueWithinOneSecond(t *testing.T) {
	h := newHost(t)
	a, err := h.keepCopy([]byte("a"), "state-migrated")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.keepCopy([]byte("b"), "state-migrated")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !bytes.Equal(read(t, a), []byte("a")) {
		t.Fatalf("copies %s and %s collide", a, b)
	}
	if info, _ := os.Stat(a); info.Mode().Perm() != secretMode {
		t.Fatalf("copy mode %v", info.Mode())
	}
}
