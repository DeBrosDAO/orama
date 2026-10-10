package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// StagedFile is a file the chain install places in the cosmovisor layout
// beside oramad: the shielded verifier. Sum is the SHA-256 the installer read.
type StagedFile struct {
	Name string
	Src  string
	Sum  string
}

// verifyStagedVerifier reads the staged shielded verifier and its digest file,
// holds both to the release manifest, holds the verifier to the digest file,
// and holds oramad (oramad's bytes) to the verifier: oramad runs no verifier
// but the one whose digest was linked into it, and refuses to start on a chain
// that is not a localnet without one, so a pair that does not match is refused
// here rather than at the first start. It returns the verifier's digest.
func verifyStagedVerifier(stagedDir string, manifest stagedManifest, oramad []byte) (string, error) {
	staged := rootfs.At(stagedDir)
	verifier, err := staged.ReadFile(filepath.Join(stagedDir, globalVerifierBinary), globalBinaryLimit)
	if err != nil {
		return "", fmt.Errorf("read the staged %s (the release ships it beside oramad; put it in %s): %w", globalVerifierBinary, stagedDir, err)
	}
	if err := manifest.verify(globalVerifierBinary, verifier); err != nil {
		return "", err
	}
	digest, err := staged.ReadFile(filepath.Join(stagedDir, globalVerifierDigest), verifierDigestLimit)
	if err != nil {
		return "", fmt.Errorf("read the staged %s (the release ships it beside the verifier): %w", globalVerifierDigest, err)
	}
	if err := manifest.verify(globalVerifierDigest, digest); err != nil {
		return "", err
	}
	sum := sha256.Sum256(verifier)
	hexSum := hex.EncodeToString(sum[:])
	if got := strings.TrimSpace(string(digest)); got != hexSum {
		return "", fmt.Errorf("%s says %s but the staged %s is %s", globalVerifierDigest, got, globalVerifierBinary, hexSum)
	}
	if !bytes.Contains(oramad, []byte(hexSum)) {
		return "", fmt.Errorf("the staged oramad does not pin the staged %s (sha256 %s): it would refuse to start with it; stage the verifier of the same release", globalVerifierBinary, hexSum)
	}
	return hexSum, nil
}

// stageGenesisInLayout is GlobalHost.StageGenesis on a real node: root stages
// src and the companions into the chain home's cosmovisor layout, verifying
// through each staged copy's own descriptor that its bytes are the ones the
// installer hashed.
func stageGenesisInLayout(home string) func(src, sum string, companions []StagedFile) (string, error) {
	return func(src, sum string, companions []StagedFile) (string, error) {
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
			return dst, stageMissingCompanions(layout, companions)
		}
		return layout.StageGenesis(src, hashVerify(sum), layoutCompanions(companions)...)
	}
}

// stageMissingCompanions adds the companions an installed genesis binary lacks.
// One that is there with other bytes is refused, as a different oramad is.
func stageMissingCompanions(layout cosmovisor.Layout, companions []StagedFile) error {
	var missing []cosmovisor.Companion
	for _, c := range layoutCompanions(companions) {
		path := filepath.Join(layout.GenesisBinDir(), c.Name)
		have, err := stagedBinaryHasSum(path, sumOf(companions, c.Name))
		if err != nil {
			return err
		}
		if !have {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return layout.StageGenesisCompanions(missing...)
}

func layoutCompanions(files []StagedFile) []cosmovisor.Companion {
	out := make([]cosmovisor.Companion, len(files))
	for i, f := range files {
		out[i] = cosmovisor.Companion{Name: f.Name, Src: f.Src, Verify: hashVerify(f.Sum)}
	}
	return out
}

func sumOf(files []StagedFile, name string) string {
	for _, f := range files {
		if f.Name == name {
			return f.Sum
		}
	}
	return ""
}

func hashVerify(sum string) cosmovisor.Verify {
	return func(f *os.File) error { return fileHasSum(f, sum) }
}
