package install

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

const (
	// globalBinaryLimit bounds a staged binary read into the installer.
	globalBinaryLimit = 512 << 20
	// globalBinaryMode and globalUnitMode are what the installer writes.
	globalBinaryMode = 0o755
	globalUnitMode   = 0o644
	// globalDirMode is the bin directory and the state root: root's, readable
	// and traversable by the service accounts.
	globalDirMode = 0o755
)

// GlobalHost is where the global installer writes and how it runs commands.
// DefaultGlobalHost is the real node; a test points every path at a
// temporary directory and runs a fake.
type GlobalHost struct {
	Run       func(name string, args ...string) ([]byte, error)
	BinRoot   rootfs.Root
	BinDir    string
	UnitRoot  rootfs.Root
	UnitDir   string
	StateRoot rootfs.Root
	StateDir  string
	ChainHome string
	Lookup    func(account string) (uid, gid int, err error)
	Chown     func(r rootfs.Root, path string, uid, gid int) error
	Logf      func(format string, args ...any)
}

// DefaultGlobalHost is this machine. The anchors are directories only root
// may write: /usr/lib, the unit directory, and /var/lib.
func DefaultGlobalHost(logf func(format string, args ...any)) GlobalHost {
	return GlobalHost{
		Run:       runCommand,
		BinRoot:   rootfs.At(filepath.Dir(filepath.Dir(constants.GlobalBinDir))),
		BinDir:    constants.GlobalBinDir,
		UnitRoot:  rootfs.At(systemdUnitDir),
		UnitDir:   systemdUnitDir,
		StateRoot: rootfs.At(filepath.Dir(constants.GlobalStateRoot)),
		StateDir:  constants.GlobalStateRoot,
		ChainHome: constants.ChainHome,
		Lookup:    cosmovisor.LookupAccount,
		Chown:     func(r rootfs.Root, path string, uid, gid int) error { return r.Chown(path, uid, gid) },
		Logf:      logf,
	}
}

// InstallGlobal puts the chosen global services on this node: accounts,
// binaries, the chain home when asked, units (enabled, not started) and the
// public firewall rules. Every step is idempotent; run it again with the same
// options and nothing changes but the binaries' bytes.
func InstallGlobal(opts GlobalInstallOptions, h GlobalHost) error {
	if err := opts.validate(); err != nil {
		return err
	}
	active, err := checkGlobalFirewall(h.Run, opts.EnableFirewall, opts.SSHPort)
	if err != nil {
		return err
	}
	if err := ensureGlobalAccounts(h.Run, opts.Services); err != nil {
		return err
	}
	if err := installGlobalBinaries(h, opts.StagedDir, opts.binaries()); err != nil {
		return err
	}
	if err := h.StateRoot.MkdirAll(h.StateDir, globalDirMode); err != nil {
		return fmt.Errorf("create %s: %w", h.StateDir, err)
	}
	if opts.InitChain != nil {
		if err := initChainHome(h, *opts.InitChain); err != nil {
			return err
		}
	}
	if err := writeGlobalUnits(h, opts); err != nil {
		return err
	}
	return applyGlobalFirewall(h.Run, opts.firewall(), active, opts.SSHPort)
}

// ensureGlobalAccounts creates each service's system account and the extra
// groups its unit names.
func ensureGlobalAccounts(run commandRunner, services []GlobalService) error {
	for _, s := range services {
		spec := globalServiceSpecs[s]
		if err := ensureServiceAccount(run, spec.user); err != nil {
			return err
		}
		for _, group := range spec.groups {
			if err := ensureSystemGroup(run, group); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureSystemGroup creates the system group name unless it exists.
func ensureSystemGroup(run commandRunner, name string) error {
	exists, err := accountEntryExists(run, "group", name)
	if err != nil || exists {
		return err
	}
	if out, err := run("groupadd", "--system", name); err != nil {
		return fmt.Errorf("create the %s group: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installGlobalBinaries copies each binary from the staged directory into
// the bin directory, root-owned 0755. The staged directory is an anchor: as
// root, one that is not root's or is writable by others is refused, and a
// symlink in it is never followed. The bin directory is made root's 0755.
func installGlobalBinaries(h GlobalHost, stagedDir string, names []string) error {
	staged := rootfs.At(stagedDir)
	if err := h.BinRoot.MkdirAll(h.BinDir, globalDirMode); err != nil {
		return fmt.Errorf("create %s: %w", h.BinDir, err)
	}
	for _, dir := range []string{filepath.Dir(h.BinDir), h.BinDir} {
		if err := rootOwned(h, dir, globalDirMode); err != nil {
			return err
		}
	}
	for _, name := range names {
		data, err := staged.ReadFile(filepath.Join(stagedDir, name), globalBinaryLimit)
		if err != nil {
			return fmt.Errorf("read the staged %s (put the release's %s in %s): %w", name, name, stagedDir, err)
		}
		dst := filepath.Join(h.BinDir, name)
		if err := h.BinRoot.WriteFile(dst, data, globalBinaryMode); err != nil {
			return fmt.Errorf("install %s: %w", dst, err)
		}
		if err := rootOwned(h, dst, globalBinaryMode); err != nil {
			return err
		}
		h.Logf("  ✓ %s installed", dst)
	}
	return nil
}

// rootOwned makes path, below BinRoot, owned by root with mode.
func rootOwned(h GlobalHost, path string, mode fs.FileMode) error {
	if err := h.Chown(h.BinRoot, path, 0, 0); err != nil {
		return fmt.Errorf("chown %s to root: %w", path, err)
	}
	if err := h.BinRoot.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// writeGlobalUnits writes each service's unit, reloads systemd and enables
// the units. It does not start them: `orama global start` does, in order.
func writeGlobalUnits(h GlobalHost, opts GlobalInstallOptions) error {
	var units []string
	for _, s := range opts.Services {
		unit := globalServiceSpecs[s].unit
		path := filepath.Join(h.UnitDir, unit)
		if err := h.UnitRoot.WriteFile(path, []byte(opts.unit(s)), globalUnitMode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		units = append(units, unit)
	}
	if out, err := h.Run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, unit := range units {
		if out, err := h.Run("systemctl", "enable", unit); err != nil {
			return fmt.Errorf("enable %s: %w\n%s", unit, err, strings.TrimSpace(string(out)))
		}
		h.Logf("  ✓ %s written and enabled", unit)
	}
	return nil
}
