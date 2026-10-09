package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
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
	// LookupGroup is the gid of a system group.
	LookupGroup func(group string) (gid int, err error)
	// Arch is the GOARCH that picks the pinned cosmovisor tarball.
	Arch string
	// CosmovisorPins is the pinned SHA-256 of the cosmovisor tarball, by GOARCH.
	CosmovisorPins map[string]string
	// StageGenesis puts the staged oramad in the cosmovisor layout as the
	// genesis binary; sum is the SHA-256 of src. It is idempotent for the
	// same bytes.
	StageGenesis func(src, sum string) (string, error)
	Chown        func(r rootfs.Root, path string, uid, gid int) error
	Logf         func(format string, args ...any)
	// Netns is the co-located layout's host; the zero value is a global-only
	// machine and is never consulted without opts.Colocated.
	Netns NetnsHost
	// InstallTor installs or upgrades the Tor package from the Tor Project's
	// repository and masks the distro's own units. It needs the network.
	InstallTor func() error
}

// DefaultGlobalHost is this machine. The anchors are directories only root
// may write: /usr/lib, the unit directory, and /var/lib.
func DefaultGlobalHost(logf func(format string, args ...any)) GlobalHost {
	return GlobalHost{
		Run:            runCommand,
		BinRoot:        rootfs.At(filepath.Dir(filepath.Dir(constants.GlobalBinDir))),
		BinDir:         constants.GlobalBinDir,
		UnitRoot:       rootfs.At(systemdUnitDir),
		UnitDir:        systemdUnitDir,
		StateRoot:      rootfs.At(filepath.Dir(constants.GlobalStateRoot)),
		StateDir:       constants.GlobalStateRoot,
		ChainHome:      constants.ChainHome,
		Lookup:         cosmovisor.LookupAccount,
		LookupGroup:    lookupGroupID,
		Arch:           runtime.GOARCH,
		CosmovisorPins: constants.CosmovisorTarballSHA256,
		StageGenesis:   stageGenesisInLayout(constants.ChainHome),
		Chown:          func(r rootfs.Root, path string, uid, gid int) error { return r.Chown(path, uid, gid) },
		Logf:           logf,
		Netns:          DefaultNetnsHost(runCommand),
		InstallTor: func() error {
			return installers.NewTorInstaller(runtime.GOARCH, logfWriter{logf}).EnsureInstalled()
		},
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
	reporterInstalled, err := unitInstalled(h, constants.GlobalReporterUnit)
	if err != nil {
		return err
	}
	opts.exportVotes = reporterInstalled || slices.Contains(opts.Services, GlobalServiceReporter)
	torPlan, err := planGlobalTor(h, opts)
	if err != nil {
		return err
	}
	reporter, err := planGlobalReporter(opts, torPlan)
	if err != nil {
		return err
	}
	if err := requireChainForOnion(h, opts); err != nil {
		return err
	}
	plan, err := planColocation(h, opts)
	if err != nil {
		return err
	}
	active, err := checkGlobalFirewall(h.Run, opts.EnableFirewall, opts.SSHPort)
	if err != nil {
		return err
	}
	var cosmovisorBinary []byte
	if slices.Contains(opts.Services, GlobalServiceChain) {
		if cosmovisorBinary, err = preflightChain(h, opts); err != nil {
			return err
		}
	}
	if torPlan != nil {
		if err := h.InstallTor(); err != nil {
			return fmt.Errorf("install the Tor package: %w", err)
		}
	}
	if err := ensureGlobalAccounts(h.Run, opts.Services); err != nil {
		return err
	}
	sums, err := installGlobalBinaries(h, opts.StagedDir, opts.binaries())
	if err != nil {
		return err
	}
	if slices.Contains(opts.Services, GlobalServiceChain) {
		if err := installCosmovisor(h, cosmovisorBinary); err != nil {
			return err
		}
	}
	if err := h.StateRoot.MkdirAll(h.StateDir, globalDirMode); err != nil {
		return fmt.Errorf("create %s: %w", h.StateDir, err)
	}
	if opts.InitChain != nil {
		if err := initChainHome(h, *opts.InitChain); err != nil {
			return err
		}
	}
	if plan != nil {
		if err := writeNetns(h, plan); err != nil {
			return err
		}
	}
	if slices.Contains(opts.Services, GlobalServiceChain) {
		if err := stageGenesisBinary(h, filepath.Join(opts.StagedDir, globalOramadBinary), sums[globalOramadBinary]); err != nil {
			return err
		}
	}
	if slices.Contains(opts.Services, GlobalServiceIPFS) {
		if err := installPublicKubo(h, opts.PublicStorageBytes, opts.Colocated); err != nil {
			return err
		}
	}
	if torPlan != nil {
		if err := applyGlobalTor(h, torPlan); err != nil {
			return err
		}
	}
	if reporter != nil {
		if err := applyGlobalReporter(h, reporter); err != nil {
			return err
		}
	}
	if err := writeGlobalUnits(h, opts); err != nil {
		return err
	}
	if err := applyGlobalFirewall(h.Run, opts.firewall(), active, opts.SSHPort); err != nil {
		return err
	}
	if plan == nil {
		return recordGlobalRole(h.Netns)
	}
	if err := savePreferencesBoth(h.Netns, plan.prefs); err != nil {
		return fmt.Errorf("record the co-located role: %w", err)
	}
	if slices.Contains(opts.Services, GlobalServiceIPFS) {
		h.Logf("  ! the public Kubo's RPC listens on %s:%d in the namespace and the provider and GC units now call it there; on a node that was already running, restart them so they pick it up: orama global restart (Kubo starts before the provider)", constants.GlobalNetnsAddr, constants.GlobalIPFSAPIPort)
	}
	h.Logf("  ! the cluster gateway reads the chain's listeners when it starts, so its /v1/chain/ route follows the namespace address only after this node restarts (orama node restart, one node at a time: it is not done here because it takes the node's quorum duties with it); the node report already follows")
	return nil
}

// planColocation is the co-location decision, made before the host changes: a
// plan for --colocated, nil for a global-only install, and a refusal for a
// global-only install on a machine that is already co-located.
func planColocation(h GlobalHost, opts GlobalInstallOptions) (*netnsPlan, error) {
	if opts.Colocated {
		return planNetns(h, opts)
	}
	if h.Netns.OramaDir == "" {
		return nil, nil
	}
	return nil, refuseGlobalOnlyOnColocated(h.Netns)
}

// ensureGlobalAccounts creates each service's system account and the extra
// groups its unit names.
func ensureGlobalAccounts(run commandRunner, services []GlobalService) error {
	for _, s := range services {
		spec := globalServiceSpecs[s]
		for _, user := range append([]string{spec.user}, spec.users...) {
			if err := ensureServiceAccount(run, user); err != nil {
				return err
			}
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
//
// It returns the SHA-256 (hex) of each installed binary by name.
func installGlobalBinaries(h GlobalHost, stagedDir string, names []string) (map[string]string, error) {
	staged := rootfs.At(stagedDir)
	if err := h.BinRoot.MkdirAll(h.BinDir, globalDirMode); err != nil {
		return nil, fmt.Errorf("create %s: %w", h.BinDir, err)
	}
	for _, dir := range []string{filepath.Dir(h.BinDir), h.BinDir} {
		if err := rootOwned(h, dir, globalDirMode); err != nil {
			return nil, err
		}
	}
	sums := map[string]string{}
	for _, name := range names {
		data, err := staged.ReadFile(filepath.Join(stagedDir, name), globalBinaryLimit)
		if err != nil {
			return nil, fmt.Errorf("read the staged %s (put the release's %s in %s): %w", name, name, stagedDir, err)
		}
		dst := filepath.Join(h.BinDir, name)
		if err := h.BinRoot.WriteFile(dst, data, globalBinaryMode); err != nil {
			return nil, fmt.Errorf("install %s: %w", dst, err)
		}
		if err := rootOwned(h, dst, globalBinaryMode); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		sums[name] = hex.EncodeToString(sum[:])
		h.Logf("  ✓ %s installed", dst)
	}
	return sums, nil
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

// writeGlobalUnits writes each service's unit files, reloads systemd and
// enables the units. It does not start them: `orama global start` does, in
// order.
func writeGlobalUnits(h GlobalHost, opts GlobalInstallOptions) error {
	var enable []string
	for _, s := range opts.Services {
		files, err := opts.unitFilesFor(s)
		if err != nil {
			return err
		}
		for _, u := range files {
			path := filepath.Join(h.UnitDir, u.name)
			if err := h.UnitRoot.WriteFile(path, []byte(u.body), globalUnitMode); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			if u.enable {
				enable = append(enable, u.name)
			}
		}
	}
	if opts.Colocated {
		enable = append(enable, globalnetns.UnitName)
	}
	if out, err := h.Run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, unit := range enable {
		if out, err := h.Run("systemctl", "enable", unit); err != nil {
			return fmt.Errorf("enable %s: %w\n%s", unit, err, strings.TrimSpace(string(out)))
		}
		h.Logf("  ✓ %s written and enabled", unit)
	}
	return nil
}

// lookupGroupID is the gid of a system group.
func lookupGroupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up the %s group: %w", name, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("the %s group has a non-numeric gid %q: %w", name, g.Gid, err)
	}
	return gid, nil
}

// unitInstalled reports whether the unit file is in the unit directory.
func unitInstalled(h GlobalHost, unit string) (bool, error) {
	path := filepath.Join(h.UnitDir, unit)
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s: %w", path, err)
	}
	return true, nil
}

// requireChainForOnion refuses an onion service on a machine with no chain: the
// tx gate behind it forwards to this host's chain REST API. The chain may be in
// this install or already installed.
func requireChainForOnion(h GlobalHost, opts GlobalInstallOptions) error {
	if !slices.Contains(opts.Services, GlobalServiceOnion) || slices.Contains(opts.Services, GlobalServiceChain) {
		return nil
	}
	path := filepath.Join(h.UnitDir, constants.ChainServiceUnit)
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("the onion service forwards to this host's chain, and %s is not installed: add chain to --services (%w)", path, err)
	}
	return nil
}
