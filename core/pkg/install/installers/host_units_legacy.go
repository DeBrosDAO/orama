package installers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Before every host daemon ran as an orama-namespace-*@index instance, install
// wrote a unit of its own for each of them into /etc/systemd/system. Install
// writes none of them now; orama-node's supervisor stops and disables them on
// every boot so they cannot race the @index instances for their ports, which
// only works while the unit files are there to be found. LegacyHostUnitCleaner
// removes them from nodes installed before, on every install and upgrade, and
// does nothing on a node that never had them.

// LegacyHostUnits are the pre-namespace host units install used to write,
// stopped, disabled and deleted in this order: a timer before the service it
// triggers, and ipfs-cluster before the ipfs daemon it requires.
//
// The names are fixed and name files directly in the unit directory, so the
// paths the cleaner deletes are known at compile time.
var LegacyHostUnits = []string{
	"orama-ipfs-gc.timer",
	"orama-ipfs-gc.service",
	"orama-ipfs-cluster.service",
	"orama-ipfs.service",
	"orama-olric.service",
	"orama-vault.service",
	"coredns.service",
	"caddy.service",
	"ntfy.service",
	"orama-sni-router.service",
}

// hostUnitDir is where install wrote the legacy units.
const hostUnitDir = "/etc/systemd/system"

// LegacyHostUnitCleaner removes the pre-namespace host units from a node.
type LegacyHostUnitCleaner struct {
	run       commandRunner
	root      string
	logWriter io.Writer
}

// NewLegacyHostUnitCleaner creates a cleaner for this machine.
func NewLegacyHostUnitCleaner(logWriter io.Writer) *LegacyHostUnitCleaner {
	return &LegacyHostUnitCleaner{run: execRunner, root: "/", logWriter: logWriter}
}

// Remove stops, disables and deletes every legacy host unit whose file is
// still in the unit directory. The caller reloads systemd afterwards.
func (c *LegacyHostUnitCleaner) Remove() error {
	for _, unit := range LegacyHostUnits {
		if err := c.removeUnit(unit); err != nil {
			return err
		}
	}
	return nil
}

// removeUnit retires one unit. Its file is what install wrote, so a unit
// without one is not ours — a package's own unit under /lib/systemd — and is
// left alone. A masked unit (`orama node stop` masks what it stops) is
// unmasked first: disable refuses a masked unit, and the mask is a /dev/null
// symlink in the unit directory that unmasking removes.
func (c *LegacyHostUnitCleaner) removeUnit(unit string) error {
	path := filepath.Join(c.root, hostUnitDir, unit)
	present, err := c.present(path)
	if err != nil || !present {
		return err
	}

	state, err := c.loadState(unit)
	if err != nil {
		return err
	}
	if state == "masked" {
		if err := runChecked(c.run, "systemctl", "unmask", unit); err != nil {
			return fmt.Errorf("unmask legacy host unit %s: %w", unit, err)
		}
		fmt.Fprintf(c.logWriter, "    ✓ Unmasked legacy %s\n", unit)
		if present, err = c.present(path); err != nil || !present {
			return err
		}
	}
	// A unit systemd has not loaded (its file arrived without a daemon-reload)
	// is not running; disable still removes its enablement links.
	if state != "not-found" {
		if err := runChecked(c.run, "systemctl", "stop", unit); err != nil {
			return fmt.Errorf("stop legacy host unit %s: %w", unit, err)
		}
	}
	if err := runChecked(c.run, "systemctl", "disable", unit); err != nil {
		return fmt.Errorf("disable legacy host unit %s: %w", unit, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove legacy host unit %s: %w", path, err)
	}
	fmt.Fprintf(c.logWriter, "    ✓ Removed legacy %s\n", unit)
	return nil
}

// present reports whether path exists as a file or a symlink.
func (c *LegacyHostUnitCleaner) present(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy host unit %s: %w", path, err)
	}
	return true, nil
}

// loadState is systemd's LoadState for unit: "loaded", "masked", "not-found",
// or another.
func (c *LegacyHostUnitCleaner) loadState(unit string) (string, error) {
	out, err := c.run("systemctl", "show", "-p", "LoadState", "--value", unit)
	if err != nil {
		return "", fmt.Errorf("read the load state of legacy host unit %s: %w (%s)", unit, err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}
