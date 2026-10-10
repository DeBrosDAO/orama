package install

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// RequirePublicKubo refuses a node that has no public Kubo, which is what
// SetPublicStorage needs; it changes nothing.
func RequirePublicKubo(h GlobalHost) error {
	installed, err := unitInstalled(h, constants.GlobalIPFSUnit)
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("the public Kubo is not installed on this node (%s is missing), so there is no storage to resize", constants.GlobalIPFSUnit)
	}
	return nil
}

// SetPublicStorage sizes the public Kubo for storageBytes of declared capacity
// (StorageMax is that plus headroom), keeping the repo's identity, token and
// the rest of its config. The running Kubo reads the config when it restarts;
// the caller restarts it, and declares the same capacity on the chain.
func SetPublicStorage(h GlobalHost, storageBytes uint64, colocated bool) error {
	if err := RequirePublicKubo(h); err != nil {
		return err
	}
	return installPublicKubo(h, storageBytes, colocated)
}

// RelayExitChange is a switch of the Tor relay between a plain relay and an exit,
// worked out and checked before anything is written.
type RelayExitChange struct {
	path string
	next string
	// Changed is false when the relay already is in the role asked for.
	Changed bool
}

// PlanRelayExit works out the relay's torrc for the role, under the network file
// the relay was installed with and the exit operator's reject list, and refuses
// what cannot be done (no relay, an authority, a network that forbids exits). It
// writes nothing.
func PlanRelayExit(h GlobalHost, exit bool) (RelayExitChange, error) {
	relay, err := unitInstalled(h, constants.GlobalTorRelayUnit)
	if err != nil {
		return RelayExitChange{}, err
	}
	if !relay {
		return RelayExitChange{}, fmt.Errorf("this node runs no Tor relay (%s is missing), so it has no exit role to switch", constants.GlobalTorRelayUnit)
	}
	path := filepath.Join(h.StateDir, filepath.Base(constants.GlobalTorrcFor(constants.GlobalTorRelayHome)))
	current, err := h.StateRoot.ReadFile(path, torRejectLimit)
	if err != nil {
		return RelayExitChange{}, fmt.Errorf("read the relay's torrc %s: %w", path, err)
	}
	network, err := readInstalledTorNetwork(h)
	if err != nil {
		return RelayExitChange{}, err
	}
	var reject []string
	if exit {
		if reject, err = readExitRejectList(h); err != nil {
			return RelayExitChange{}, err
		}
	}
	next, err := tornet.SetExit(string(current), exit, reject, network)
	if err != nil {
		return RelayExitChange{}, err
	}
	return RelayExitChange{path: path, next: next, Changed: next != string(current)}, nil
}

// Apply writes the planned torrc; the caller restarts the relay. A change that
// changes nothing writes nothing.
func (c RelayExitChange) Apply(h GlobalHost) error {
	if !c.Changed {
		return nil
	}
	if err := h.StateRoot.WriteFile(c.path, []byte(c.next), torrcMode); err != nil {
		return fmt.Errorf("write %s: %w", c.path, err)
	}
	return nil
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
