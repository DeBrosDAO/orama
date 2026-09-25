package installers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The Anyone network (the `anon` package, its relay and client) was removed in
// favour of Tor. Nodes installed before that still carry its units, package,
// apt source and state. LegacyAnyoneCleaner removes all of it; it runs on every
// install and upgrade before Tor is installed, and does nothing on a node that
// never had Anyone.

// LegacyAnyoneUnits are the Anyone units a node may still have, stopped and
// disabled in this order. The namespace instance goes first: it is the one
// that was actually running and holding the SOCKS port Tor now binds.
var LegacyAnyoneUnits = []string{
	"orama-namespace-anyone-client@index.service",
	"orama-anyone-client.service",
	"orama-anyone-relay.service",
	"anon.service",
}

// LegacyAnyonePackages are purged, together with their configuration. nyx was
// installed only to watch the anon control port; the Tor client has none.
var LegacyAnyonePackages = []string{"anon", "nyx"}

// LegacyAnyonePaths are every file and directory the Anyone install left:
// the apt source and key, its config, state (including relay identity keys)
// and logs, the unit files Orama wrote, and the index env and log files.
func LegacyAnyonePaths(oramaDir string) []string {
	return []string{
		"/etc/apt/sources.list.d/anon.list",
		"/etc/apt/trusted.gpg.d/anon.asc",
		"/etc/anon",
		"/var/lib/anon",
		"/var/log/anon",
		"/etc/logrotate.d/anon",
		"/etc/systemd/system/orama-anyone-client.service",
		"/etc/systemd/system/orama-anyone-relay.service",
		"/etc/systemd/system/orama-namespace-anyone-client@.service",
		filepath.Join(oramaDir, "data", "namespaces", "index", "anyone-client.env"),
		filepath.Join(oramaDir, "logs", "anyone-client.log"),
	}
}

// LegacyAnyoneCleaner removes the Anyone network from a node.
type LegacyAnyoneCleaner struct {
	run       commandRunner
	root      string
	oramaDir  string
	logWriter io.Writer
}

// NewLegacyAnyoneCleaner creates a cleaner for the node whose Orama state
// lives in oramaDir.
func NewLegacyAnyoneCleaner(oramaDir string, logWriter io.Writer) *LegacyAnyoneCleaner {
	return &LegacyAnyoneCleaner{run: execRunner, root: "/", oramaDir: oramaDir, logWriter: logWriter}
}

// Remove stops and disables the Anyone units, purges the package and deletes
// its files. Every step checks before it acts, so running it twice, or on a
// node that never had Anyone, is a no-op.
func (c *LegacyAnyoneCleaner) Remove() error {
	for _, unit := range LegacyAnyoneUnits {
		if err := c.stopUnit(unit); err != nil {
			return err
		}
	}
	for _, pkg := range LegacyAnyonePackages {
		if err := c.purgePackage(pkg); err != nil {
			return err
		}
	}
	for _, p := range LegacyAnyonePaths(c.oramaDir) {
		path := filepath.Join(c.root, p)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove legacy Anyone path %s: %w", path, err)
		}
		fmt.Fprintf(c.logWriter, "    ✓ Removed %s\n", path)
	}
	if out, err := c.run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload after removing the Anyone units: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

// stopUnit stops and disables unit unless systemd does not know it. A masked
// unit (`orama node stop` masks every service it stops) is unmasked first:
// disable refuses a masked unit, and the mask's /dev/null symlink would
// otherwise outlive the unit.
func (c *LegacyAnyoneCleaner) stopUnit(unit string) error {
	out, err := c.run("systemctl", "show", "-p", "LoadState", "--value", unit)
	if err != nil {
		return fmt.Errorf("read the load state of %s: %w (%s)", unit, err, strings.TrimSpace(out))
	}
	switch strings.TrimSpace(out) {
	case "not-found":
		return nil
	case "masked":
		if out, err := c.run("systemctl", "unmask", unit); err != nil {
			return fmt.Errorf("unmask legacy Anyone unit %s: %w (%s)", unit, err, strings.TrimSpace(out))
		}
	}
	if out, err := c.run("systemctl", "stop", unit); err != nil {
		return fmt.Errorf("stop legacy Anyone unit %s: %w (%s)", unit, err, strings.TrimSpace(out))
	}
	if out, err := c.run("systemctl", "disable", unit); err != nil {
		return fmt.Errorf("disable legacy Anyone unit %s: %w (%s)", unit, err, strings.TrimSpace(out))
	}
	fmt.Fprintf(c.logWriter, "    ✓ Stopped and disabled %s\n", unit)
	return nil
}

// purgePackage purges pkg if dpkg knows it in any state but not-installed.
func (c *LegacyAnyoneCleaner) purgePackage(pkg string) error {
	out, err := c.run("dpkg-query", "-W", "-f=${db:Status-Status}", pkg)
	if err != nil {
		if strings.Contains(out, "no packages found") {
			return nil
		}
		return fmt.Errorf("query the dpkg status of %s: %w (%s)", pkg, err, strings.TrimSpace(out))
	}
	if status := strings.TrimSpace(out); status == "" || status == "not-installed" {
		return nil
	}
	if err := aptGet(c.run, "purge", "-y", pkg); err != nil {
		return fmt.Errorf("purge legacy Anyone package %s: %w", pkg, err)
	}
	fmt.Fprintf(c.logWriter, "    ✓ Purged package %s\n", pkg)
	return nil
}
