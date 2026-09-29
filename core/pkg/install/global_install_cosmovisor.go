package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// cosmovisorTarballLimit bounds the staged cosmovisor release tarball.
const cosmovisorTarballLimit = 128 << 20

// verifyCosmovisor reads the staged official release tarball
// (constants.CosmovisorTarball), requires its SHA-256 to equal the pinned
// digest for this architecture, so the bytes are the release the pin names and
// nothing else, and returns the tarball's cosmovisor file. It changes nothing
// on the host.
func verifyCosmovisor(h GlobalHost, stagedDir string) ([]byte, error) {
	pin, ok := h.CosmovisorPins[h.Arch]
	if !ok {
		return nil, fmt.Errorf("no cosmovisor %s is pinned for %s; the global chain runs on linux amd64 or arm64", constants.CosmovisorVersion, h.Arch)
	}
	name := constants.CosmovisorTarball(h.Arch)
	tarball, err := rootfs.At(stagedDir).ReadFile(filepath.Join(stagedDir, name), cosmovisorTarballLimit)
	if err != nil {
		return nil, fmt.Errorf("read the staged cosmovisor release (download %s from the cosmos-sdk release cosmovisor/%s into %s): %w",
			name, constants.CosmovisorVersion, stagedDir, err)
	}
	sum := sha256.Sum256(tarball)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(pin)) != 1 {
		return nil, fmt.Errorf("%s has sha256 %x, not the pinned %s; it is not the official cosmovisor %s release", name, sum, pin, constants.CosmovisorVersion)
	}
	binary, err := extractCosmovisor(tarball)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return binary, nil
}

// installCosmovisor writes the verified cosmovisor file to the bin directory,
// root-owned 0755.
func installCosmovisor(h GlobalHost, binary []byte) error {
	dst := filepath.Join(h.BinDir, constants.CosmovisorBinary)
	if err := h.BinRoot.WriteFile(dst, binary, globalBinaryMode); err != nil {
		return fmt.Errorf("install %s: %w", dst, err)
	}
	if err := rootOwned(h, dst, globalBinaryMode); err != nil {
		return err
	}
	h.Logf("  ✓ %s installed (cosmovisor %s)", dst, constants.CosmovisorVersion)
	return nil
}

// extractCosmovisor reads the cosmovisor executable out of a release tarball.
func extractCosmovisor(tarball []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("not a gzip file: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s file in the archive", constants.CosmovisorBinary)
		}
		if err != nil {
			return nil, fmt.Errorf("read the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || strings.TrimPrefix(hdr.Name, "./") != constants.CosmovisorBinary {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, globalBinaryLimit+1))
		if err != nil {
			return nil, fmt.Errorf("read %s from the archive: %w", hdr.Name, err)
		}
		if len(data) > globalBinaryLimit {
			return nil, fmt.Errorf("%s in the archive is over %d bytes", hdr.Name, globalBinaryLimit)
		}
		return data, nil
	}
}

// preflightChain runs every check that can refuse the chain install before
// anything on the host changes: the staged cosmovisor tarball against its pin
// (returning the cosmovisor file), a chain home with a genesis to stage oramad
// for (unless --init-chain will create it), and a staged oramad that matches
// the genesis binary already in the cosmovisor layout.
func preflightChain(h GlobalHost, opts GlobalInstallOptions) ([]byte, error) {
	cosmovisorBinary, err := verifyCosmovisor(h, opts.StagedDir)
	if err != nil {
		return nil, err
	}
	if opts.InitChain == nil {
		if _, err := os.Lstat(filepath.Join(h.ChainHome, "config", "genesis.json")); err != nil {
			return nil, fmt.Errorf("the chain home %s has no genesis, so there is nothing to stage oramad for; run with --init-chain and --genesis, or restore the node's home first", h.ChainHome)
		}
	}
	data, err := rootfs.At(opts.StagedDir).ReadFile(filepath.Join(opts.StagedDir, globalOramadBinary), globalBinaryLimit)
	if err != nil {
		return nil, fmt.Errorf("read the staged %s (put the release's %s in %s): %w", globalOramadBinary, globalOramadBinary, opts.StagedDir, err)
	}
	sum := sha256.Sum256(data)
	layout := cosmovisor.Layout{Home: h.ChainHome, Daemon: constants.ChainDaemonName}
	if _, err := stagedBinaryHasSum(layout.GenesisBinary(), hex.EncodeToString(sum[:])); err != nil {
		return nil, err
	}
	return cosmovisorBinary, nil
}

// stageGenesisBinary puts oramad in the cosmovisor layout as the genesis
// binary, which is what `current` points at until an upgrade. sum is the
// SHA-256 of the staged oramad the installer read. A genesis binary already
// there is left alone when it is the same bytes (a second install run) and
// refused when it is not: a chain binary is changed with
// `orama global stage-oramad --upgrade`, never by staging over the layout.
func stageGenesisBinary(h GlobalHost, src, sum string) error {
	dst, err := h.StageGenesis(src, sum)
	if err != nil {
		return err
	}
	h.Logf("  ✓ oramad staged at %s", dst)
	return nil
}

// stageGenesisInLayout is GlobalHost.StageGenesis on a real node: root stages
// src into the chain home's cosmovisor layout, verifying through the staged
// copy's own descriptor that its bytes are the ones the installer hashed.
func stageGenesisInLayout(home string) func(src, sum string) (string, error) {
	return func(src, sum string) (string, error) {
		uid, gid, err := cosmovisor.LookupAccount(constants.ChainUser)
		if err != nil {
			return "", err
		}
		layout := cosmovisor.Layout{Home: home, Daemon: constants.ChainDaemonName, ChainUID: uid, ChainGID: gid}
		dst := layout.GenesisBinary()
		same, err := stagedBinaryHasSum(dst, sum)
		if err != nil {
			return "", err
		}
		if same {
			return dst, nil
		}
		return layout.StageGenesis(src, func(f *os.File) error { return fileHasSum(f, sum) })
	}
}

// stagedBinaryHasSum reports whether the file at path has SHA-256 sum. A
// missing file is false. The file's directory (genesis/bin, root's) anchors
// the read, and a symlink at the file is refused.
func stagedBinaryHasSum(path, sum string) (bool, error) {
	data, err := rootfs.At(filepath.Dir(path)).ReadFile(path, globalBinaryLimit)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the staged oramad %s: %w", path, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != sum {
		return false, fmt.Errorf("%s is already staged with different bytes than the staged oramad; change the chain binary with 'orama global stage-oramad --upgrade <plan>', not by installing again", path)
	}
	return true, nil
}

func fileHasSum(f *os.File, sum string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %s: %w", f.Name(), err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", f.Name(), err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("sha256 %s, want %s", got, sum)
	}
	return nil
}
