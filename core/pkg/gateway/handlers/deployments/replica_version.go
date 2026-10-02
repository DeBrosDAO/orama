package deployments

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// versionKind is which counter a replica keeps for a deployment.
type versionKind string

const (
	// deployVersion is the deployment's version (build or content), bumped by an
	// update or a rollback.
	deployVersion versionKind = "deploy"
	// envVersion is the stamp of the last environment change (nextEnvVersion).
	envVersion versionKind = "env"
)

// errStaleVersion is a change older than the one this replica already applied.
var errStaleVersion = errors.New("a newer version is already applied on this node")

// appliedVersionPath is where a replica records the newest version of kind it
// applied. It sits beside the deployment's directory, not in it: an update
// swaps the directory for a freshly extracted one.
func appliedVersionPath(deployPath string, kind versionKind) string {
	return deployPath + "." + string(kind) + "-version"
}

// checkVersionNotStale refuses a change older than the one applied. Two
// changes made in order can arrive out of order (a retry overtaking a slow
// call), and applying the older last would put the replica back on a state the
// home node has already left. A zero version is an unversioned caller and is
// not checked; no recorded version means nothing has been applied yet.
func checkVersionNotStale(deployPath string, kind versionKind, version int64) error {
	if version == 0 {
		return nil
	}
	applied, err := readAppliedVersion(deployPath, kind)
	if err != nil {
		return err
	}
	if version < applied {
		return fmt.Errorf("%w: %s version %d is older than the applied %d", errStaleVersion, kind, version, applied)
	}
	return nil
}

func readAppliedVersion(deployPath string, kind versionKind) (int64, error) {
	data, err := os.ReadFile(appliedVersionPath(deployPath, kind))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read the applied %s version: %w", kind, err)
	}
	version, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the applied %s version file is corrupt: %w", kind, err)
	}
	return version, nil
}

// recordAppliedVersion stores version as the newest applied, replacing the file
// atomically. A zero version records nothing.
func recordAppliedVersion(deployPath string, kind versionKind, version int64) error {
	if version == 0 {
		return nil
	}
	path := appliedVersionPath(deployPath, kind)
	// A temp file of its own: two gateways on one host record the same
	// deployment, and a shared temp name would interleave their writes.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to record the applied %s version: %w", kind, err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(strconv.FormatInt(version, 10) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to record the applied %s version: %w", kind, werr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to record the applied %s version: %w", kind, err)
	}
	return nil
}

// removeAppliedVersions deletes a torn-down replica's records.
func removeAppliedVersions(deployPath string) {
	for _, kind := range []versionKind{deployVersion, envVersion} {
		_ = os.Remove(appliedVersionPath(deployPath, kind))
	}
}

// sweepStaleVersionTemps removes the temp files a record that died half way
// (a crash between create and rename) left beside the deployments. It runs when
// the handler starts, before any record is written, so no live write is
// removed. It returns how many it removed.
func sweepStaleVersionTemps(baseDeployPath string) (int, error) {
	stale, err := filepath.Glob(filepath.Join(baseDeployPath, "*-version.*.tmp"))
	if err != nil {
		return 0, fmt.Errorf("failed to list stale version files in %s: %w", baseDeployPath, err)
	}
	removed := 0
	for _, path := range stale {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("failed to remove the stale version file %s: %w", path, err)
		}
		removed++
	}
	return removed, nil
}
