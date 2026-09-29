package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

const (
	// genesisLimit bounds the genesis file the operator hands the installer.
	genesisLimit = 64 << 20
	// chainHomeMode is the chain home: the chain account's alone.
	chainHomeMode = 0o700
	// genesisMode is genesis.json as oramad init writes it.
	genesisMode = 0o600
)

// initChainHome creates the chain home, runs `oramad init` as the chain
// account, and replaces the genesis it wrote with the network's. It refuses a
// home that already has a genesis: initialising twice would discard a node's
// keys, so an existing home is never touched.
func initChainHome(h GlobalHost, in ChainInit) error {
	genesis, err := readGenesis(in.GenesisPath, in.ChainID)
	if err != nil {
		return err
	}
	genesisPath := filepath.Join(h.ChainHome, "config", "genesis.json")
	if _, err := os.Lstat(genesisPath); err == nil {
		return fmt.Errorf("%s already exists; --init-chain never re-initialises a chain home, so drop --init-chain", genesisPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", genesisPath, err)
	}
	uid, gid, err := h.Lookup(constants.ChainUser)
	if err != nil {
		return err
	}
	if err := h.StateRoot.MkdirAll(h.ChainHome, chainHomeMode); err != nil {
		return fmt.Errorf("create %s: %w", h.ChainHome, err)
	}
	if err := h.Chown(h.StateRoot, h.ChainHome, uid, gid); err != nil {
		return fmt.Errorf("chown %s to %s: %w", h.ChainHome, constants.ChainUser, err)
	}
	oramad := filepath.Join(h.BinDir, globalOramadBinary)
	out, err := h.Run("runuser", "-u", constants.ChainUser, "--", oramad, "init", in.Moniker,
		"--chain-id", in.ChainID, "--default-denom", constants.ChainDenom, "--home", h.ChainHome)
	if err != nil {
		return fmt.Errorf("oramad init as %s: %w\n%s", constants.ChainUser, err, strings.TrimSpace(string(out)))
	}
	if err := h.StateRoot.WriteFile(genesisPath, genesis, genesisMode); err != nil {
		return fmt.Errorf("install the network genesis at %s: %w", genesisPath, err)
	}
	if err := h.Chown(h.StateRoot, genesisPath, uid, gid); err != nil {
		return fmt.Errorf("chown %s to %s: %w", genesisPath, constants.ChainUser, err)
	}
	h.Logf("  ✓ chain home %s initialised for %s", h.ChainHome, in.ChainID)
	return nil
}

// readGenesis reads the operator's genesis file and checks it is for chainID.
// Its directory is the anchor: as root, one that is not root's or that others
// may write is refused, and the file itself is not followed if a symlink.
func readGenesis(path, chainID string) ([]byte, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("genesis path %q must be absolute", path)
	}
	data, err := rootfs.At(filepath.Dir(path)).ReadFile(path, genesisLimit)
	if err != nil {
		return nil, fmt.Errorf("read the genesis %s: %w", path, err)
	}
	var doc struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("genesis %s is not JSON: %w", path, err)
	}
	if doc.ChainID != chainID {
		return nil, fmt.Errorf("genesis %s is for chain %q, not --chain-id %q", path, doc.ChainID, chainID)
	}
	return data, nil
}

// checkGlobalFirewall reads ufw before anything on the host changes. An
// inactive ufw would make an allow rule protect nothing, so it is refused
// unless the operator asked to enable it. It reports whether ufw is active.
func checkGlobalFirewall(run commandRunner, enable bool, sshPort int) (bool, error) {
	out, err := run("ufw", "status")
	if err != nil {
		return false, fmt.Errorf("ufw status: %w (install ufw: apt-get install -y ufw)\n%s", err, strings.TrimSpace(string(out)))
	}
	if strings.Contains(string(out), "Status: active") {
		return true, nil
	}
	if !enable {
		return false, fmt.Errorf("ufw is inactive, so an allow rule would protect nothing; re-run with --enable-firewall to deny incoming by default, allow SSH on port %d and enable it", sshPort)
	}
	return false, checkSSHPort(run, sshPort)
}

// checkSSHPort confirms sshd listens on port before ufw is enabled with only
// that port allowed, from sshd's effective configuration (`sshd -T`): its
// `port N` lines and the port of its `listenaddress host:port` lines.
func checkSSHPort(run commandRunner, port int) error {
	out, err := run("sshd", "-T")
	if err != nil {
		return fmt.Errorf("read sshd's effective configuration (sshd -T) to confirm --ssh-port %d before enabling ufw: %w\n%s", port, err, strings.TrimSpace(string(out)))
	}
	ports := sshdPorts(string(out))
	if slices.Contains(ports, strconv.Itoa(port)) {
		return nil
	}
	return fmt.Errorf("sshd listens on port %s, not --ssh-port %d; enabling ufw would cut SSH, so pass the port sshd uses", strings.Join(ports, ", "), port)
}

// sshdPorts are the ports sshd listens on, from `sshd -T` output. sshd
// binds its ListenAddress entries when there are any, each with its own port
// (`listenaddress host:port`, optionally followed by `rdomain <name>`), and
// its Port entries only when there are none.
func sshdPorts(out string) []string {
	var listen, plain []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch {
		case fields[0] == "listenaddress":
			if _, p, err := net.SplitHostPort(fields[1]); err == nil && !slices.Contains(listen, p) {
				listen = append(listen, p)
			}
		case fields[0] == "port" && len(fields) == 2 && !slices.Contains(plain, fields[1]):
			plain = append(plain, fields[1])
		}
	}
	if len(listen) > 0 {
		return listen
	}
	return plain
}

// applyGlobalFirewall adds the public rules of the installed services. They
// are tagged orama-global, so a cluster reconcile never removes them. When
// ufw was inactive (and checkGlobalFirewall allowed it), it is enabled first
// with deny-incoming defaults and the SSH port allowed.
func applyGlobalFirewall(run commandRunner, fw GlobalFirewall, active bool, sshPort int) error {
	if !active {
		if err := enableGlobalFirewall(run, sshPort); err != nil {
			return err
		}
	}
	for _, args := range NewFirewallProvisioner(FirewallConfig{Global: fw}).GlobalAllowArgs() {
		if out, err := run("ufw", args...); err != nil {
			return fmt.Errorf("ufw %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// enableGlobalFirewall turns ufw on for a host that had it off. SSH is allowed
// first, so enabling it does not cut the operator's session.
func enableGlobalFirewall(run commandRunner, sshPort int) error {
	for _, args := range [][]string{
		{"default", "deny", "incoming"},
		{"default", "allow", "outgoing"},
		{"allow", strconv.Itoa(sshPort) + "/tcp", "comment", GlobalRuleComment},
		{"--force", "enable"},
	} {
		if out, err := run("ufw", args...); err != nil {
			return fmt.Errorf("ufw %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
