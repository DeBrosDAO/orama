package installers

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseTarball writes a tarball shaped like the nodejs.org release: bin/node
// a file, bin/npm and bin/npx symlinks into lib, as upstream ships them.
func releaseTarball(t *testing.T, top string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	write(&tar.Header{Name: top + "/bin/node", Mode: 0o755, Typeflag: tar.TypeReg}, "#!/bin/sh\n")
	write(&tar.Header{Name: top + "/lib/node_modules/npm/bin/npm-cli.js", Mode: 0o755, Typeflag: tar.TypeReg}, "//npm\n")
	write(&tar.Header{Name: top + "/lib/node_modules/npm/bin/npx-cli.js", Mode: 0o755, Typeflag: tar.TypeReg}, "//npx\n")
	for _, cmd := range []string{"npm", "npx"} {
		if err := tw.WriteHeader(&tar.Header{Name: top + "/bin/" + cmd, Typeflag: tar.TypeSymlink,
			Linkname: "../lib/node_modules/npm/bin/" + cmd + "-cli.js"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func testNodeInstaller(t *testing.T, arch string) *NodeJSInstaller {
	ni := NewNodeJSInstaller(arch, io.Discard)
	ni.root, ni.binDir = t.TempDir(), t.TempDir()
	return ni
}

// The deployment units run /usr/bin/node and /usr/bin/npm, which nothing
// installed (stagenet e2e, 2026-09-30).
func TestNodeJS_unpackAndLinkMakeEveryCommandResolve(t *testing.T) {
	ni := testNodeInstaller(t, "amd64")
	dir, err := ni.releaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if ni.IsInstalled() {
		t.Fatal("an empty node reports Node.js installed")
	}
	if err := ni.unpack(releaseTarball(t, filepath.Base(dir)), dir); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if err := ni.link(dir); err != nil {
		t.Fatalf("link: %v", err)
	}
	if !ni.IsInstalled() {
		t.Fatal("Node.js does not report installed after unpack and link")
	}
	for _, cmd := range nodeCommands {
		if _, err := os.Stat(filepath.Join(ni.binDir, cmd)); err != nil {
			t.Errorf("%s does not resolve: %v", cmd, err)
		}
	}
	// Idempotent: linking again replaces the links in place.
	if err := ni.link(dir); err != nil || !ni.IsInstalled() {
		t.Fatalf("relink: %v", err)
	}
}

func TestNodeJS_aReleaseWithoutNodeIsRefusedAndNothingIsLinked(t *testing.T) {
	ni := testNodeInstaller(t, "arm64")
	dir, _ := ni.releaseDir()
	if err := ni.unpack(releaseTarball(t, "some-other-top"), dir); err == nil {
		t.Fatal("a release with no bin/node was accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused release left %s behind", dir)
	}
}

func TestVerifyNodeTarball(t *testing.T) {
	if err := verifyNodeTarball("amd64", []byte("not the release")); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("a tampered tarball was accepted: %v", err)
	}
	if err := verifyNodeTarball("riscv64", nil); err == nil {
		t.Error("an arch with no pinned digest was accepted")
	}
	if _, err := testNodeInstaller(t, "riscv64").releaseDir(); err == nil {
		t.Error("an unsupported arch has a release dir")
	}
}
