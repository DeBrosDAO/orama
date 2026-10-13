package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/planinfo"
)

// What ChainUpgrade.Checksums says about the staged files.
const (
	// ChecksumsMatch: the staged oramad and verifier are the ones the plan names.
	ChecksumsMatch = "matches"
	// ChecksumsMismatch: a staged file is not the one the plan names; cosmovisor would switch the
	// node to a binary the proposal did not name.
	ChecksumsMismatch = "mismatch"
	// ChecksumsUnnamed: the plan names no usable checksum for this platform, so the staged files
	// cannot be checked against it.
	ChecksumsUnnamed = "unnamed"
	// ChecksumsUnchecked: the staged files could not be read.
	ChecksumsUnchecked = "unchecked"
)

// currentPlanPath is x/upgrade's query for the plan the chain has scheduled.
const currentPlanPath = "/cosmos/upgrade/v1beta1/current_plan"

// chainHome is the cosmovisor DAEMON_HOME the staged binaries are looked for in. It is a variable
// so tests can stand in for the node.
var chainHome = constants.ChainHome

// ChainUpgrade is the software upgrade the chain has scheduled, and whether this node holds the
// binary for it. cosmovisor switches to a staged binary at Height; with none staged the chain
// halts there until one is.
type ChainUpgrade struct {
	Name   string `json:"name"`
	Height int64  `json:"height"`
	// Staged is whether upgrades/<Name>/bin holds both the chain binary and the shielded verifier
	// that rides with it.
	Staged bool `json:"staged"`
	// StageError is why Staged could not be decided (a plan name cosmovisor cannot stage, a
	// layout that could not be read). Empty when Staged is true or the binary is simply absent.
	StageError string `json:"stage_error,omitempty"`
	// Checksums is whether the staged files are the ones the plan's info names (ChecksumsMatch,
	// ChecksumsMismatch, ChecksumsUnnamed or ChecksumsUnchecked); empty while nothing is staged.
	Checksums string `json:"checksums,omitempty"`
	// ChecksumProblem says which file does not match, or why the check could not be made. Empty
	// when Checksums is empty or ChecksumsMatch.
	ChecksumProblem string `json:"checksum_problem,omitempty"`
	// info is the plan's info, the source of the checksums; it is not reported.
	info string
}

// readChainUpgrade fills r.Upgrade from the chain's REST API at apiBase and the cosmovisor layout.
// A failed query is UpgradeError and leaves the RPC section responsive, like the signing queries.
func readChainUpgrade(ctx context.Context, apiBase string, r *ChainReport) {
	r.Upgrade, r.UpgradeError = nil, ""
	body, err := httpGet(ctx, apiBase+currentPlanPath)
	if err != nil {
		r.UpgradeError = shortChainError(fmt.Errorf("read the scheduled upgrade: %w", err))
		return
	}
	plan, err := parseChainUpgrade(body)
	if err != nil {
		r.UpgradeError = shortChainError(err)
		return
	}
	if plan == nil {
		return
	}
	plan.Staged, plan.StageError = upgradeStaged(chainHome, plan.Name)
	if plan.Staged {
		plan.Checksums, plan.ChecksumProblem = stagedChecksums(chainHome, plan.Name, plan.info)
	}
	r.Upgrade = plan
}

// parseChainUpgrade reads x/upgrade's current_plan answer; nil when no upgrade is scheduled.
// Protobuf JSON carries the int64 height as a string.
func parseChainUpgrade(body []byte) (*ChainUpgrade, error) {
	var resp struct {
		Plan *struct {
			Name   string `json:"name"`
			Height int64  `json:"height,string"`
			Info   string `json:"info"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("the chain answered a malformed upgrade plan: %w", err)
	}
	if resp.Plan == nil || resp.Plan.Name == "" {
		return nil, nil
	}
	return &ChainUpgrade{Name: resp.Plan.Name, Height: resp.Plan.Height, info: resp.Plan.Info}, nil
}

// upgradeStaged reports whether home's cosmovisor layout holds the binary and the verifier for the
// plan name. A name cosmovisor cannot stage is reported, not treated as absent.
func upgradeStaged(home, name string) (staged bool, problem string) {
	layout := cosmovisor.Layout{Home: home, Daemon: constants.ChainDaemonName}
	binary, err := layout.UpgradeBinary(name)
	if err != nil {
		return false, shortChainError(err)
	}
	verifier := filepath.Join(filepath.Dir(binary), constants.ChainVerifierBinary)
	for _, path := range []string{binary, verifier} {
		info, err := os.Lstat(path)
		switch {
		case os.IsNotExist(err):
			return false, ""
		case err != nil:
			return false, shortChainError(fmt.Errorf("check %s: %w", path, err))
		case !info.Mode().IsRegular():
			return false, shortChainError(fmt.Errorf("%s is not a regular file", path))
		}
	}
	return true, ""
}

// stagedChecksums holds the oramad and the verifier staged for the plan name to the checksums the
// plan's info names for this platform. The files are only read when they changed since the last
// report: an oramad is large and a report is frequent.
func stagedChecksums(home, name, info string) (checksums, problem string) {
	want, err := planinfo.Parse(name, info, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		return ChecksumsUnnamed, shortChainError(err)
	}
	layout := cosmovisor.Layout{Home: home, Daemon: constants.ChainDaemonName}
	binary, err := layout.UpgradeBinary(name)
	if err != nil {
		return ChecksumsUnchecked, shortChainError(err)
	}
	oramad, err := cachedSHA256(binary)
	if err != nil {
		return ChecksumsUnchecked, shortChainError(err)
	}
	verifier, err := cachedSHA256(filepath.Join(filepath.Dir(binary), constants.ChainVerifierBinary))
	if err != nil {
		return ChecksumsUnchecked, shortChainError(err)
	}
	switch {
	case oramad != want.Oramad && verifier != want.Verifier:
		return ChecksumsMismatch, "the staged oramad and verifier are not the ones the plan names"
	case oramad != want.Oramad:
		return ChecksumsMismatch, "the staged oramad is not the one the plan names"
	case verifier != want.Verifier:
		return ChecksumsMismatch, "the staged verifier is not the one the plan names"
	}
	return ChecksumsMatch, ""
}

// fileDigest is a file's sha256 as of the size and modification time it was read at.
type fileDigest struct {
	size    int64
	modTime time.Time
	sum     string
}

var (
	digestMu    sync.Mutex
	digestCache = map[string]fileDigest{}
)

// cachedSHA256 is the sha256 of the regular file at path, read again only when its size or
// modification time changed.
func cachedSHA256(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("check %s: %w", path, err)
	}
	digestMu.Lock()
	cached, ok := digestCache[path]
	digestMu.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	digestMu.Lock()
	digestCache[path] = fileDigest{size: info.Size(), modTime: info.ModTime(), sum: sum}
	digestMu.Unlock()
	return sum, nil
}
