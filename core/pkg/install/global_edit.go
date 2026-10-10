package install

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// SetPublicStorage sizes the public Kubo for storageBytes of declared capacity
// (StorageMax is that plus headroom), keeping the repo's identity, token and
// the rest of its config. The running Kubo reads the config when it restarts;
// the caller restarts it, and declares the same capacity on the chain.
func SetPublicStorage(h GlobalHost, storageBytes uint64, colocated bool) error {
	installed, err := unitInstalled(h, constants.GlobalIPFSUnit)
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("the public Kubo is not installed on this node (%s is missing), so there is no storage to resize", constants.GlobalIPFSUnit)
	}
	return installPublicKubo(h, storageBytes, colocated)
}

// SetRelayExit switches the node's Tor relay between a plain relay and an exit,
// rewriting only the exit section of its torrc, under the network file the
// relay was installed with and the exit operator's reject list. It reports
// whether the torrc changed; the caller restarts the relay when it did.
func SetRelayExit(h GlobalHost, exit bool) (changed bool, err error) {
	relay, err := unitInstalled(h, constants.GlobalTorRelayUnit)
	if err != nil {
		return false, err
	}
	if !relay {
		return false, fmt.Errorf("this node runs no Tor relay (%s is missing), so it has no exit role to switch", constants.GlobalTorRelayUnit)
	}
	torrcPath := filepath.Join(h.StateDir, filepath.Base(constants.GlobalTorrcFor(constants.GlobalTorRelayHome)))
	current, err := h.StateRoot.ReadFile(torrcPath, torRejectLimit)
	if err != nil {
		return false, fmt.Errorf("read the relay's torrc %s: %w", torrcPath, err)
	}
	network, err := readInstalledTorNetwork(h)
	if err != nil {
		return false, err
	}
	var reject []string
	if exit {
		if reject, err = readExitRejectList(h); err != nil {
			return false, err
		}
	}
	next, err := tornet.SetExit(string(current), exit, reject, network)
	if err != nil {
		return false, err
	}
	if next == string(current) {
		return false, nil
	}
	if err := h.StateRoot.WriteFile(torrcPath, []byte(next), torrcMode); err != nil {
		return false, fmt.Errorf("write %s: %w", torrcPath, err)
	}
	return true, nil
}

// readInstalledTorNetwork is the Tor network file the install wrote.
func readInstalledTorNetwork(h GlobalHost) (tornet.Network, error) {
	path := filepath.Join(h.StateDir, constants.GlobalTorAuthoritiesFile)
	raw, err := h.StateRoot.ReadFile(path, tornet.NetworkFileLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return tornet.Network{}, fmt.Errorf("the Tor network file %s is missing: the relay was not installed by `orama maint global install`", path)
	}
	if err != nil {
		return tornet.Network{}, fmt.Errorf("read %s: %w", path, err)
	}
	return tornet.ParseNetwork(raw)
}
