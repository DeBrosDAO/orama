package chainfaucet

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestKey_signsADocumentTheChainWouldVerify(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte("a sign document")

	sig, err := key.SignOramaTx(context.Background(), doc)

	if err != nil {
		t.Fatal(err)
	}
	if len(sig.Signature) != 64 || sig.Address != key.Address() {
		t.Fatalf("signature %d bytes for %s", len(sig.Signature), sig.Address)
	}
	pub, err := secp256k1.ParsePubKey(sig.PubKey)
	if err != nil {
		t.Fatal(err)
	}
	var r, s secp256k1.ModNScalar
	r.SetByteSlice(sig.Signature[:32])
	s.SetByteSlice(sig.Signature[32:])
	sum := sha256.Sum256(doc)
	if !ecdsa.NewSignature(&r, &s).Verify(sum[:], pub) {
		t.Error("the signature does not verify against the key's public key over SHA-256 of the document")
	}
	if s.IsOverHalfOrder() {
		t.Error("s is high: the chain refuses a high-s signature")
	}
}

func TestKey_theAddressIsTheAccountOfThePublicKey(t *testing.T) {
	key, _ := NewKey()
	acct, err := key.OramaAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, err := clusterreg.AccountAddressOf(acct.PubKey)
	if err != nil || acct.Address != want || key.Address() != want {
		t.Errorf("address %s, public key's account %s, %v", acct.Address, want, err)
	}
	if _, err := clusterreg.CanonicalAccount(acct.Address); err != nil {
		t.Errorf("the faucet's address is not canonical: %v", err)
	}
	acct.PubKey[0] ^= 0xff
	again, _ := key.OramaAccount(context.Background())
	if again.PubKey[0] == acct.PubKey[0] {
		t.Error("a caller changed the key's public key through the account it was given")
	}
}

func TestKey_twoKeysAreTwoAccounts(t *testing.T) {
	a, _ := NewKey()
	b, _ := NewKey()
	if a.Address() == b.Address() {
		t.Fatal("NewKey made the same key twice")
	}
}

func TestParseKey(t *testing.T) {
	key, _ := NewKey()
	back, err := ParseKey(key.encode())
	if err != nil || back.Address() != key.Address() {
		t.Fatalf("a key does not survive its file: %v", err)
	}
	for name, content := range map[string]string{
		"empty":     "",
		"not hex":   strings.Repeat("zz", 32),
		"too short": strings.Repeat("ab", 31),
		"too long":  strings.Repeat("ab", 33),
		"all zero":  strings.Repeat("00", 32),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseKey([]byte(content)); err == nil {
				t.Fatal("ParseKey accepted it")
			}
		})
	}
}

func writeKeyFile(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), KeyFileName)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadKey_readsAKeyOnlyItsOwnerCanRead(t *testing.T) {
	key, _ := NewKey()
	path := writeKeyFile(t, KeyFileMode, string(key.encode()))

	got, err := LoadKey(path)

	if err != nil || got.Address() != key.Address() {
		t.Fatalf("LoadKey = %v, %v", got, err)
	}
}

func TestLoadKey_refusesAFileThatIsNotSafeToTrustWithASecret(t *testing.T) {
	key, _ := NewKey()
	content := string(key.encode())
	for name, mode := range map[string]os.FileMode{"group readable": 0o640, "world readable": 0o644, "group writable": 0o620, "world writable": 0o602} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadKey(writeKeyFile(t, mode, content))
			if err == nil {
				t.Fatal("a key file anyone could read or change was loaded")
			}
			mustContain(t, err.Error(), "chmod 0600")
		})
	}
	t.Run("a symlink", func(t *testing.T) {
		real := writeKeyFile(t, KeyFileMode, content)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		_, err := LoadKey(link)
		if err == nil {
			t.Fatal("a symlink was followed to a key")
		}
		mustContain(t, err.Error(), "symlink")
	})
	t.Run("a directory", func(t *testing.T) {
		if _, err := LoadKey(t.TempDir()); err == nil {
			t.Fatal("a directory was read as a key")
		}
	})
	t.Run("a FIFO", func(t *testing.T) {
		fifo := filepath.Join(t.TempDir(), "fifo")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skipf("no FIFO here: %v", err)
		}
		done := make(chan error, 1)
		go func() { _, err := LoadKey(fifo); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a FIFO was read as a key")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("LoadKey blocked on a FIFO: the gateway would hang at its start")
		}
	})
	t.Run("a file that is not there", func(t *testing.T) {
		_, err := LoadKey(filepath.Join(t.TempDir(), "absent"))
		if err == nil {
			t.Fatal("a missing key file was loaded")
		}
		mustContain(t, err.Error(), "orama maint faucet init")
	})
	t.Run("an oversized file", func(t *testing.T) {
		if _, err := LoadKey(writeKeyFile(t, KeyFileMode, strings.Repeat("a", maxKeyFileBytes+1))); err == nil {
			t.Fatal("an oversized file was read")
		}
	})
	t.Run("not a key", func(t *testing.T) {
		if _, err := LoadKey(writeKeyFile(t, KeyFileMode, "hello\n")); err == nil {
			t.Fatal("garbage was read as a key")
		}
	})
}

func TestLoadKey_theErrorNeverContainsTheSecret(t *testing.T) {
	key, _ := NewKey()
	secret := strings.TrimSpace(string(key.encode()))
	_, err := LoadKey(writeKeyFile(t, 0o644, secret+"\n"))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
	_, err = LoadKey(writeKeyFile(t, KeyFileMode, secret+"zz\n"))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateKeyFile_makesAKeyOnlyItsOwnerCanReadAndNeverReplacesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", KeyFileName)

	key, created, err := CreateKeyFile(path, os.Geteuid(), os.Getegid())

	if err != nil || !created {
		t.Fatalf("CreateKeyFile = created %v, %v", created, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != KeyFileMode {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	loaded, err := LoadKey(path)
	if err != nil || loaded.Address() != key.Address() {
		t.Fatalf("the file does not hold the key it reported: %v", err)
	}
	again, created, err := CreateKeyFile(path, os.Geteuid(), os.Getegid())
	if err != nil || created || again.Address() != key.Address() {
		t.Fatalf("a second run replaced the key: created %v, %v", created, err)
	}
}

func TestCreateKeyFile_refusesToReplaceAFileThatIsNotAKey(t *testing.T) {
	path := writeKeyFile(t, KeyFileMode, "somebody's file\n")
	if _, _, err := CreateKeyFile(path, os.Geteuid(), os.Getegid()); err == nil {
		t.Fatal("a file that is not a key was replaced")
	}
	if got, _ := os.ReadFile(path); string(got) != "somebody's file\n" {
		t.Errorf("the file was changed: %q", got)
	}
}

func TestCreateKeyFile_aSymlinkToAKeyIsNotTrusted(t *testing.T) {
	key, _ := NewKey()
	real := writeKeyFile(t, KeyFileMode, string(key.encode()))
	link := filepath.Join(t.TempDir(), KeyFileName)
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, created, err := CreateKeyFile(link, os.Geteuid(), os.Getegid())
	if err == nil || created {
		t.Fatalf("init printed the account of a key the gateway would refuse to load: created %v, %v", created, err)
	}
	mustContain(t, err.Error(), "symlink")
}

func TestCreateKeyFile_aSymlinkIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, KeyFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateKeyFile(link, os.Geteuid(), os.Getegid()); err == nil {
		t.Fatal("a key was written through a symlink")
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the symlink's target was created")
	}
}
