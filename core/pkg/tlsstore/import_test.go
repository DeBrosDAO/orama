package tlsstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacy lays out Caddy's file storage under dir.
func writeLegacy(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func openStored(t *testing.T, s *Store, k Keys, key string) string {
	t.Helper()
	sealed, err := s.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("load %s: %v", key, err)
	}
	got, err := Open(k.Seal, key, sealed)
	if err != nil {
		t.Fatalf("open %s: %v", key, err)
	}
	return string(got)
}

func TestImportLegacy_sealsCertificatesAndAccount(t *testing.T) {
	s, _ := newTestStore(t)
	k := vectorKeys(t)
	dir := t.TempDir()
	writeLegacy(t, dir, map[string]string{
		"certificates/ca/wildcard_.x/wildcard_.x.crt": "CERT",
		"certificates/ca/wildcard_.x/wildcard_.x.key": "KEY",
		"acme/ca/users/a/a.key":                       "ACCOUNT KEY",
		"locks/issue_cert_x.lock":                     "lock",
		"ocsp/x":                                      "staple",
		"last_clean.json":                             "{}",
	})
	res, err := s.ImportLegacy(context.Background(), k.Seal, dir)
	if err != nil || res.Added != 3 || len(res.Skipped) != 0 {
		t.Fatalf("ImportLegacy = %+v, %v; want the three certificate and account files", res, err)
	}
	if got := openStored(t, s, k, "certificates/ca/wildcard_.x/wildcard_.x.key"); got != "KEY" {
		t.Errorf("imported key opens to %q", got)
	}
	keys, _ := s.List(context.Background(), "", false)
	for _, k := range keys {
		if k == "locks" || k == "ocsp" || k == "last_clean.json" {
			t.Errorf("imported %s, which is not state", k)
		}
	}
}

// A name is imported whole or not at all: a node importing a directory the
// store already holds anything under adds nothing to it, so a certificate is
// never stored beside another node's key.
func TestImportLegacy_aDirectoryIsImportedWholeOrNotAtAll(t *testing.T) {
	s, _ := newTestStore(t)
	k := vectorKeys(t)
	first := t.TempDir()
	writeLegacy(t, first, map[string]string{"certificates/ca/x/x.crt": "CERT A"})
	if _, err := s.ImportLegacy(context.Background(), k.Seal, first); err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	writeLegacy(t, second, map[string]string{
		"certificates/ca/x/x.crt":  "CERT B",
		"certificates/ca/x/x.key":  "KEY B",
		"certificates/ca/x/x.json": "{}",
		"certificates/ca/y/y.crt":  "CERT Y",
	})
	res, err := s.ImportLegacy(context.Background(), k.Seal, second)
	if err != nil || res.Added != 1 {
		t.Fatalf("ImportLegacy = %+v, %v; want only certificates/ca/y imported", res, err)
	}
	if got := openStored(t, s, k, "certificates/ca/x/x.crt"); got != "CERT A" {
		t.Errorf("the store's certificate was replaced: %q", got)
	}
	if _, err := s.Load(context.Background(), "certificates/ca/x/x.key"); !IsNotExist(err) {
		t.Errorf("another node's key was stored beside the first node's certificate: %v", err)
	}
}

func TestImportLegacy_nothingToImport(t *testing.T) {
	s, _ := newTestStore(t)
	res, err := s.ImportLegacy(context.Background(), vectorKeys(t).Seal, filepath.Join(t.TempDir(), "absent"))
	if err != nil || res.Added != 0 {
		t.Fatalf("ImportLegacy of a missing dir = %+v, %v", res, err)
	}
}

// A stray file that is not CertMagic's is skipped and named; it does not keep
// the store closed.
func TestImportLegacy_skipsWhatIsNotCertMagics(t *testing.T) {
	s, _ := newTestStore(t)
	dir := t.TempDir()
	writeLegacy(t, dir, map[string]string{
		"certificates/ca/x/x.crt":      "CERT",
		"certificates/ca/x/x.crt~ bak": "editor backup",
		"certificates/ca/big/big.crt":  strings.Repeat("A", MaxValueLen+1),
	})
	res, err := s.ImportLegacy(context.Background(), vectorKeys(t).Seal, dir)
	if err != nil || res.Added != 1 || len(res.Skipped) != 2 {
		t.Fatalf("ImportLegacy = %+v, %v; want one added and two skipped", res, err)
	}
}

// A symlink in Caddy's storage is never followed out of it.
func TestImportLegacy_neverFollowsASymlinkOut(t *testing.T) {
	s, _ := newTestStore(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLegacy(t, dir, map[string]string{"certificates/ca/x/x.crt": "CERT"})
	if err := os.Symlink(outside, filepath.Join(dir, "certificates/ca/x/x.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "certificates/ca/link")); err != nil {
		t.Fatal(err)
	}
	res, err := s.ImportLegacy(context.Background(), vectorKeys(t).Seal, dir)
	if err != nil || res.Added != 1 || len(res.Skipped) != 2 {
		t.Fatalf("ImportLegacy = %+v, %v; want the certificate alone and both symlinks named", res, err)
	}
	if _, err := s.Load(context.Background(), "certificates/ca/x/x.key"); !IsNotExist(err) {
		t.Error("a symlink out of Caddy's storage was imported")
	}
	// Read directly, a path through a symlink that leaves the tree is refused.
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, _, err := readLegacyFile(root, vectorKeys(t).Seal, "certificates/ca/x/x.key"); err == nil {
		t.Error("readLegacyFile followed a symlink out of the tree")
	}
}

func TestImportLegacy_unreadableFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	s, _ := newTestStore(t)
	dir := t.TempDir()
	writeLegacy(t, dir, map[string]string{"certificates/ca/x/x.key": "KEY"})
	if err := os.Chmod(filepath.Join(dir, "certificates/ca/x/x.key"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportLegacy(context.Background(), vectorKeys(t).Seal, dir); err == nil {
		t.Fatal("an unreadable certificate file was skipped silently")
	}
}
