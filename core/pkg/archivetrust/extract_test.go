package archivetrust

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type tarEntry struct {
	name     string
	body     string
	typeflag byte // 0: what `orama build` writes (no type set)
	link     string
	size     int64 // header size when body is empty; 0 means len(body)
}

func writeTarball(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		size := int64(len(e.body))
		if e.size != 0 {
			size = e.size
		}
		if e.typeflag != 0 && e.typeflag != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o777, Size: size, Typeflag: e.typeflag, Linkname: e.link}); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Flush()
	gz.Close()
	f.Close()
	return path
}

func TestExtract_setsModesWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dest := t.TempDir()
	archive := writeTarball(t, []tarEntry{{name: "bin/", typeflag: tar.TypeDir}, {name: "bin/orama", body: "cli"}})
	if err := Extract(archive, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for path, want := range map[string]os.FileMode{"bin": 0o755, "bin/orama": 0o755} {
		info, err := os.Stat(filepath.Join(dest, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s has mode %#o, want %#o (group- or world-writable bits dropped, umask ignored)", path, info.Mode().Perm(), want)
		}
	}
}

func TestExtract_refusesEntriesAnArchiveMustNotHave(t *testing.T) {
	for name, entry := range map[string]tarEntry{
		"symlink":          {name: "bin/orama", typeflag: tar.TypeSymlink, link: "/bin/sh"},
		"hard link":        {name: "bin/orama", typeflag: tar.TypeLink, link: "manifest.json"},
		"absolute path":    {name: "/etc/cron.d/x", body: "x"},
		"parent directory": {name: "../etc/x", body: "x"},
		"escaping clean":   {name: "bin/../../x", body: "x"},
		"node data":        {name: ".orama/secrets/cluster-secret", body: "x"},
		"trust anchor":     {name: "etc/orama/archive-signers", body: "x"},
		"too deep":         {name: "bin/sub/x", body: "x"},
		"device":           {name: "bin/tty", typeflag: tar.TypeChar},
		"oversized":        {name: "bin/huge", typeflag: tar.TypeReg, size: MaxFileBytes + 1},
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			archive := writeTarball(t, []tarEntry{{name: ManifestName, body: "{}"}, entry})
			if err := Extract(archive, dest); err == nil {
				t.Fatalf("extracted %+v", entry)
			}
			if _, err := os.Lstat(filepath.Join(dest, "bin", "huge")); err == nil {
				t.Fatal("an oversized entry was written")
			}
		})
	}
}

func TestExtract_refusesADuplicateEntry(t *testing.T) {
	archive := writeTarball(t, []tarEntry{{name: "bin/orama", body: "a"}, {name: "./bin/orama", body: "b"}})
	if err := Extract(archive, t.TempDir()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("two entries for one path must be refused, got %v", err)
	}
}

func TestExtract_refusesTooManyEntries(t *testing.T) {
	entries := make([]tarEntry, 0, maxEntries+1)
	for i := 0; i <= maxEntries; i++ {
		entries = append(entries, tarEntry{name: "bin/f" + strconv.Itoa(i), body: "x"})
	}
	if err := Extract(writeTarball(t, entries), t.TempDir()); err == nil {
		t.Fatal("extracted an archive with more entries than any build has")
	}
}

func TestVerifyArchiveFile_verifiesWhatIsInTheTarball(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	var entries []tarEntry
	for _, rel := range []string{ManifestName, SignatureName, "bin/orama", "bin/orama-node", "systemd/orama-namespace-x.service"} {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, tarEntry{name: rel, body: string(body)})
	}
	archive := writeTarball(t, entries)
	if v, err := VerifyArchiveFile(archive, []string{s.addr}); err != nil || v.Signer != s.addr {
		t.Fatalf("VerifyArchiveFile = %+v, %v", v, err)
	}
	if _, err := VerifyArchiveFile(archive, []string{signerA}); err == nil {
		t.Fatal("verified against a signer that did not sign")
	}
}

func TestLockArchiveDir_isExclusive(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockArchiveDir(dir)
	if err != nil {
		t.Fatalf("LockArchiveDir: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("a second holder took the archive lock while the first held it")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
}

// signedTarball is a tarball of a signed test archive, as `orama build`
// writes one.
func signedTarball(t *testing.T, s testSigner) string {
	t.Helper()
	dir := writeTestArchive(t, s, testFiles, nil)
	var entries []tarEntry
	for _, rel := range []string{ManifestName, SignatureName, "bin/orama", "bin/orama-node", "systemd/orama-namespace-x.service"} {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, tarEntry{name: rel, body: string(body)})
	}
	return writeTarball(t, entries)
}

func TestPrepareUpload_writesACanonicalArchiveOfTheVerifiedTree(t *testing.T) {
	s := newTestSigner(t)
	src := signedTarball(t, s)

	u, err := PrepareUpload(src, []string{s.addr})
	if err != nil || u.Verified.Signer != s.addr {
		t.Fatalf("PrepareUpload: %v", err)
	}
	// The source is replaced after verification: the upload must not change.
	if err := os.WriteFile(src, []byte("replaced after verification"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := VerifyArchiveFile(u.Path, []string{s.addr}); err != nil || v.Signer != s.addr {
		t.Fatalf("the canonical archive does not verify: %v", err)
	}
	f, err := os.Open(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Format != tar.FormatUSTAR || (hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir) {
			t.Errorf("%s is not a plain USTAR file or directory (%v, %c)", hdr.Name, hdr.Format, hdr.Typeflag)
		}
	}
	if err := u.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(u.Path); !os.IsNotExist(err) {
		t.Fatal("the upload was not removed")
	}
}

func TestPrepareUpload_refusesWhatDoesNotVerify(t *testing.T) {
	s := newTestSigner(t)
	if _, err := PrepareUpload(signedTarball(t, s), []string{signerA}); err == nil {
		t.Fatal("prepared an upload signed by someone the operator does not trust")
	}
}

func TestVerifyIntegrity_checksContentsAgainstWhoeverSigned(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	if v, err := VerifyIntegrity(dir); err != nil || v.Signer != s.addr {
		t.Fatalf("VerifyIntegrity = %v, %v", v, err)
	}
	writeFile(t, filepath.Join(dir, "bin", "orama"), "tampered")
	if _, err := VerifyIntegrity(dir); err == nil {
		t.Fatal("a tampered archive passed the integrity check")
	}
}
