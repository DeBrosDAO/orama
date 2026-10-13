package archivetrust

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func tarGz(t testing.TB, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		size := int64(len(e.body))
		if e.typeflag != 0 && e.typeflag != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o4777, Size: size, Typeflag: e.typeflag, Linkname: e.link}); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// treeOf lists what Extract left under dest as "path mode size" lines, and reports anything an
// archive must never produce: a link or device, a mode with setuid, setgid, sticky or a write bit
// for group or other, a path outside the archive's layout.
func treeOf(t *testing.T, dest string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dest, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			t.Fatalf("Extract left %s with mode %v", rel, info.Mode())
		}
		if info.Mode()&^(fs.ModeDir|0o755) != 0 {
			t.Fatalf("Extract left %s with mode %v", rel, info.Mode())
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > maxDepth || !slices.Contains(OwnedPaths, parts[0]) {
			t.Fatalf("Extract left %s outside the layout of an archive", rel)
		}
		out = append(out, fmt.Sprintf("%s %v %d", rel, info.Mode(), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// FuzzArchiveExtract: the build archive is a tar.gz fetched from the network and unpacked before it
// is verified. For any bytes, Extract never panics, never writes outside its destination (a
// sibling next to it stays empty), never leaves a link, a device or a file with setuid, setgid,
// sticky or group or other write bits, never leaves a path outside manifest.json, manifest.sig,
// bin, systemd and packages at two levels, and gives the same tree twice.
func FuzzArchiveExtract(f *testing.F) {
	for _, entries := range [][]tarEntry{
		{{name: "manifest.json", body: "{}"}, {name: "manifest.sig", body: "0x00"}, {name: "bin/", typeflag: tar.TypeDir}, {name: "bin/orama", body: "cli"}},
		{{name: "bin/orama", typeflag: tar.TypeSymlink, link: "/bin/sh"}},
		{{name: "../etc/x", body: "x"}},
		{{name: "/etc/cron.d/x", body: "x"}},
		{{name: "bin/../../x", body: "x"}},
		{{name: "bin/a/b/c", body: "x"}},
		{{name: "bin/orama", body: "a"}, {name: "bin/orama", body: "b"}},
		{{name: ".orama/secrets/cluster-secret", body: "x"}},
		{{name: "bin", body: "a file where a directory goes"}, {name: "bin/orama", body: "x"}},
		{{name: "bin/orama", typeflag: tar.TypeChar}},
	} {
		f.Add(tarGz(f, entries))
	}
	f.Add([]byte{})
	f.Add([]byte{0x1f, 0x8b, 0x08})
	f.Fuzz(func(t *testing.T, raw []byte) {
		root := t.TempDir()
		src := filepath.Join(root, "orama.tar.gz")
		if err := os.WriteFile(src, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		var first []string
		for run := 0; run < 2; run++ {
			parent := filepath.Join(root, "run", string(rune('a'+run)))
			dest := filepath.Join(parent, "dest")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			err := Extract(src, dest)
			tree := treeOf(t, dest)
			siblings, readErr := os.ReadDir(parent)
			if readErr != nil || len(siblings) != 1 {
				t.Fatalf("Extract wrote next to its destination (%v): %v", readErr, siblings)
			}
			if run == 0 {
				first = append(tree, errString(err))
				continue
			}
			if got := append(tree, errString(err)); !slices.Equal(first, got) {
				t.Fatalf("Extract is not deterministic:\n%v\n%v", first, got)
			}
		}
	})
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return "error"
}

// FuzzArchiveSigningMessage: the text a signer approves for a manifest cannot be bent by the
// manifest. Whatever the fields hold, an accepted message is the fixed frame, one line per field in
// order, with no field able to add a line or a character that is not printable; it is a pure
// function of the manifest bytes and commits to their SHA-256. RecoverSigner and ParseManifest
// never panic on the same bytes.
func FuzzArchiveSigningMessage(f *testing.F) {
	f.Add([]byte(`{"version":"1.2.3","commit":"abc","date":"2026-01-01T00:00:00Z","arch":"amd64","checksums":{"bin/orama":"00"}}`), "0x00")
	f.Add([]byte(`{"version":"1\nsigners: 0xattacker","commit":"","arch":"","date":""}`), "")
	f.Add([]byte("{\"version\":\"x\",\"signers\":[\"0xabc\",\"0xdef\u202e\"]}"), "0x")
	f.Add([]byte(`{"release_root":"AAAA"}`), "")
	f.Add([]byte(`null`), "")
	f.Fuzz(func(t *testing.T, manifest []byte, signature string) {
		_, _ = ParseManifest(manifest)
		_, _ = RecoverSigner(manifest, signature)
		msg, err := SigningMessage(manifest)
		if err != nil {
			return
		}
		again, _ := SigningMessage(bytes.Clone(manifest))
		if again != msg {
			t.Fatal("SigningMessage is not deterministic")
		}
		lines := strings.Split(msg, "\n")
		want := []string{"", "version: ", "commit: ", "arch: ", "date: ", "signers: "}
		if len(lines) < len(want)+1 || len(lines) > len(want)+2 {
			t.Fatalf("the message has %d lines: %q", len(lines), msg)
		}
		for i := 1; i < len(want); i++ {
			if !strings.HasPrefix(lines[i], want[i]) {
				t.Fatalf("line %d is %q, want it to open with %q", i, lines[i], want[i])
			}
		}
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "manifest sha256: ") || !strings.HasSuffix(last, ManifestDigest(manifest)) {
			t.Fatalf("the last line %q does not commit to the manifest digest", last)
		}
		for _, r := range msg {
			if r != '\n' && notPrintable(r) {
				t.Fatalf("the message carries the non-printable character %U", r)
			}
		}
	})
}

// FuzzArchiveAnchor: the trust anchor file has one form. What ParseAnchor accepts renders back to
// the same bytes, normalizes to itself, and names each address once in lowercase; NormalizeSigners
// never panics and yields distinct lowercase addresses.
func FuzzArchiveAnchor(f *testing.F) {
	f.Add([]byte("0x" + strings.Repeat("ab", 20) + "\n"))
	f.Add([]byte("0x" + strings.Repeat("ab", 20) + "\n0x" + strings.Repeat("cd", 20) + "\n"))
	f.Add([]byte("0x" + strings.Repeat("AB", 20) + "\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		signers, err := ParseAnchor(data)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if normalized, nerr := NormalizeSigners(lines); nerr == nil {
			for i, s := range normalized {
				if s != strings.ToLower(s) || slices.Index(normalized, s) != i {
					t.Fatalf("NormalizeSigners gave %q twice or in upper case", s)
				}
			}
		}
		if err != nil {
			return
		}
		// ParseAnchor tolerates a missing final newline; it accepts nothing else that renders differently.
		if rendered := formatAnchor(signers); !bytes.Equal(rendered, data) && !bytes.Equal(rendered, append(bytes.Clone(data), '\n')) {
			t.Fatalf("an accepted anchor does not render back to its bytes: %q", data)
		}
		again, err := NormalizeSigners(signers)
		if err != nil || !SameSigners(again, signers) {
			t.Fatalf("an accepted anchor does not normalize to itself (%v)", err)
		}
	})
}
