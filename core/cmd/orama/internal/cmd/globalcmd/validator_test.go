package globalcmd

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/nacl/box"
)

func newTestCmd() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	return cmd, &out
}

func TestParseServiceArgs_knownAndUnknown(t *testing.T) {
	got, err := parseServiceArgs([]string{"chain", "archiver"})
	if err != nil || len(got) != 2 || got[1] != install.GlobalServiceArchiver {
		t.Fatalf("services = %v (%v)", got, err)
	}
	if none, err := parseServiceArgs(nil); err != nil || none != nil {
		t.Fatalf("no argument should mean every installed service: %v %v", none, err)
	}
	_, err = parseServiceArgs([]string{"relay"})
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestWriteNewFile_writesPrivateAndRefusesExisting(t *testing.T) {
	cmd, _ := newTestCmd()
	path := filepath.Join(t.TempDir(), "sealed")
	if err := writeNewFile(cmd, path, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != sealedFileMode {
		t.Fatalf("mode %v (%v), want %o", info.Mode(), err, sealedFileMode)
	}
	if err := writeNewFile(cmd, path, []byte("again")); err == nil {
		t.Fatal("an existing file was overwritten")
	}
}

func TestWriteNewFile_refusesASymlink(t *testing.T) {
	cmd, _ := newTestCmd()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFile(cmd, link, []byte("sealed")); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("the symlink's target was created")
	}
}

func TestReadIdentityFile_refusesAReadableFile(t *testing.T) {
	_, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv[:])+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentityFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want a mode refusal", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readIdentityFile(path)
	if err != nil || *got != *priv {
		t.Fatalf("identity = %x (%v)", got, err)
	}
	if _, err := readIdentityFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing identity file was accepted")
	}
}

func TestRunEdit_nothingToChangeIsAUsageError(t *testing.T) {
	editFlags.operator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	t.Cleanup(func() { editFlags.operator = "" })
	err := runEdit(editCmd, nil)
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestRunEdit_badOperatorIsAUsageError(t *testing.T) {
	editFlags.operator = "cosmos1invalid"
	t.Cleanup(func() { editFlags.operator = "" })
	if err := runEdit(editCmd, nil); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestRestoreFloor_flagsGoTogether(t *testing.T) {
	t.Cleanup(func() {
		migrateImportFlags.from, migrateImportFlags.oldHostDestroyed, migrateImportFlags.floorHeight = "", false, 0
	})
	migrateImportFlags.from = "bundle"
	if floor, err := restoreFloor(); err != nil || floor != nil {
		t.Fatalf("a migration import: %v %v", floor, err)
	}
	migrateImportFlags.floorHeight = 100
	if _, err := restoreFloor(); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("--floor-height without --old-host-destroyed: %v", err)
	}
	migrateImportFlags.oldHostDestroyed = true
	floor, err := restoreFloor()
	if err != nil || floor == nil || floor.Height != 100 || floor.Step != 3 {
		t.Fatalf("restore floor = %v (%v)", floor, err)
	}
	migrateImportFlags.floorHeight = 0
	if _, err := restoreFloor(); err == nil {
		t.Fatal("--old-host-destroyed without a height was accepted")
	}
	migrateImportFlags.from = ""
	if _, err := restoreFloor(); err == nil {
		t.Fatal("no --from was accepted")
	}
}

func TestFillNewFile_failureIsReported(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "closed"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := fillNewFile(f, []byte("x")); err == nil {
		t.Fatal("a write to a closed file succeeded")
	}
}
