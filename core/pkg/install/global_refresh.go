package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// globalRunsBinary names, for the services whose long-lived process is one of
// the release's binaries, which one it is. A service not listed is not
// restarted when a binary changes: the chain runs oramad out of the cosmovisor
// layout (the orama CLI is only its pre-start check, run afresh at every
// start), and a Tor role runs the distro's tor (the CLI is its timers', run
// afresh at every firing).
var globalRunsBinary = map[GlobalService]string{
	GlobalServiceIPFS:     globalKuboBinary,
	GlobalServiceProvider: globalServiceBin,
	GlobalServiceArchiver: globalServiceBin,
	GlobalServiceIndexer:  globalServiceBin,
	GlobalServiceRepair:   globalServiceBin,
	GlobalServiceReporter: globalServiceBin,
	GlobalServiceOnion:    globalOramaCLI,
}

// RefreshOptions is one refresh of the installed global layer to the release
// staged at StagedDir.
type RefreshOptions struct {
	// Installed are the services installed on this node (their units exist).
	Installed []GlobalService
	// StagedDir is the release's bin/ and Manifest its manifest.json: every
	// file the refresh reads is held to the manifest.
	StagedDir string
	Manifest  string
}

// RefreshResult says what a refresh did and what it left alone.
type RefreshResult struct {
	// Replaced are the binaries in the global bin directory whose bytes the
	// release changed.
	Replaced []string
	// Restart are the installed services whose running binary was replaced; they
	// pick it up when restarted.
	Restart []GlobalService
	// Chain is set when the chain is installed.
	Chain *ChainBinaryState
}

// ChainBinaryState compares the oramad the chain runs with the release's.
type ChainBinaryState struct {
	// CurrentSHA256 is the oramad cosmovisor runs now, ReleaseSHA256 the release's.
	CurrentSHA256, ReleaseSHA256 string
}

// Differs reports whether the release carries another oramad than the chain runs.
func (c ChainBinaryState) Differs() bool { return c.CurrentSHA256 != c.ReleaseSHA256 }

// RefreshGlobal puts the release's global binaries in place of the installed
// ones and reports what changed. It never touches oramad: the chain binary
// changes only through a governed upgrade (StageChainUpgrade). Binaries are
// replaced atomically, so a running service keeps the file it has open until
// it is restarted, which the caller does, one node at a time.
func RefreshGlobal(h GlobalHost, opts RefreshOptions) (RefreshResult, error) {
	manifest, err := readStagedManifest(opts.Manifest)
	if err != nil {
		return RefreshResult{}, err
	}
	var res RefreshResult
	res.Replaced, err = replaceChangedBinaries(h, opts, manifest)
	if err != nil {
		return RefreshResult{}, err
	}
	for _, s := range opts.Installed {
		if bin, ok := globalRunsBinary[s]; ok && slices.Contains(res.Replaced, bin) {
			res.Restart = append(res.Restart, s)
		}
	}
	if slices.Contains(opts.Installed, GlobalServiceChain) {
		if res.Chain, err = chainBinaryState(h, opts, manifest); err != nil {
			return RefreshResult{}, err
		}
	}
	return res, nil
}

// refreshBinaries are the binaries the installed services need, except oramad.
func refreshBinaries(installed []GlobalService) []string {
	var names []string
	for _, s := range installed {
		for _, b := range globalServiceSpecs[s].binaries {
			if b != globalOramadBinary && !slices.Contains(names, b) {
				names = append(names, b)
			}
		}
	}
	slices.Sort(names)
	return names
}

// replaceChangedBinaries installs each binary whose staged bytes differ from
// the installed ones, after holding it to the manifest, and returns their names.
func replaceChangedBinaries(h GlobalHost, opts RefreshOptions, manifest stagedManifest) ([]string, error) {
	staged := rootfs.At(opts.StagedDir)
	var changed []string
	for _, name := range refreshBinaries(opts.Installed) {
		data, err := staged.ReadFile(filepath.Join(opts.StagedDir, name), globalBinaryLimit)
		if err != nil {
			return nil, fmt.Errorf("read the staged %s (the release's %s is in %s once `orama upgrade` has staged it): %w", name, name, opts.StagedDir, err)
		}
		if err := manifest.verify(name, data); err != nil {
			return nil, err
		}
		dst := filepath.Join(h.BinDir, name)
		current, err := h.BinRoot.ReadFile(dst, globalBinaryLimit)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read the installed %s: %w", dst, err)
		}
		if err == nil && bytes.Equal(current, data) {
			continue
		}
		if err := h.BinRoot.WriteFile(dst, data, globalBinaryMode); err != nil {
			return nil, fmt.Errorf("install %s: %w", dst, err)
		}
		if err := rootOwned(h, dst, globalBinaryMode); err != nil {
			return nil, err
		}
		h.Logf("  ✓ %s replaced", dst)
		changed = append(changed, name)
	}
	return changed, nil
}

// chainBinaryState reads the oramad cosmovisor runs and the release's.
func chainBinaryState(h GlobalHost, opts RefreshOptions, manifest stagedManifest) (*ChainBinaryState, error) {
	release, err := rootfs.At(opts.StagedDir).ReadFile(filepath.Join(opts.StagedDir, globalOramadBinary), globalBinaryLimit)
	if err != nil {
		return nil, fmt.Errorf("read the staged %s: %w", globalOramadBinary, err)
	}
	if err := manifest.verify(globalOramadBinary, release); err != nil {
		return nil, err
	}
	layout := cosmovisor.Layout{Home: h.ChainHome, Daemon: constants.ChainDaemonName}
	path := filepath.Join(layout.CurrentBinDir(), globalOramadBinary)
	current, err := rootfs.At(layout.CurrentBinDir()).ReadFile(path, globalBinaryLimit)
	if err != nil {
		return nil, fmt.Errorf("read the oramad cosmovisor runs (%s): %w", path, err)
	}
	return &ChainBinaryState{CurrentSHA256: sumHex(current), ReleaseSHA256: sumHex(release)}, nil
}

func sumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// StageChainUpgrade stages the release's oramad, and the shielded verifier of
// the same release, in the cosmovisor layout for the upgrade plan name, held to
// the release manifest. Cosmovisor switches to it at the plan's height. An
// oramad already staged for that name with other bytes is refused.
func StageChainUpgrade(h GlobalHost, opts RefreshOptions, name string) (string, error) {
	manifest, err := readStagedManifest(opts.Manifest)
	if err != nil {
		return "", err
	}
	oramad, err := rootfs.At(opts.StagedDir).ReadFile(filepath.Join(opts.StagedDir, globalOramadBinary), globalBinaryLimit)
	if err != nil {
		return "", fmt.Errorf("read the staged %s: %w", globalOramadBinary, err)
	}
	if err := manifest.verify(globalOramadBinary, oramad); err != nil {
		return "", err
	}
	verifierSum, err := verifyStagedVerifier(opts.StagedDir, manifest, oramad)
	if err != nil {
		return "", err
	}
	uid, gid, err := h.Lookup(constants.ChainUser)
	if err != nil {
		return "", err
	}
	layout := cosmovisor.Layout{Home: h.ChainHome, Daemon: constants.ChainDaemonName, ChainUID: uid, ChainGID: gid}
	verifier := cosmovisor.Companion{Name: globalVerifierBinary, Src: filepath.Join(opts.StagedDir, globalVerifierBinary), Verify: hashVerify(verifierSum)}
	dst, err := layout.StageUpgrade(name, filepath.Join(opts.StagedDir, globalOramadBinary), hashVerify(sumHex(oramad)), verifier)
	if err != nil {
		return "", fmt.Errorf("stage oramad for the upgrade %q: %w", name, err)
	}
	return dst, nil
}

// ChainColocated reports whether the chain listens in the orama-global
// namespace, from the namespace unit in unitDir.
func ChainColocated(unitDir string) bool {
	_, err := os.Lstat(filepath.Join(unitDir, globalnetns.UnitName))
	return err == nil
}
