package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// Bounds and names of `e2e-fleet target stagenet`.
const (
	keyscanTimeout   = 30 * time.Second
	keyscanTypes     = "ed25519,ecdsa,rsa"
	sshPort          = 22
	stagenetRPCURL   = "http://" + config.StagenetChainHost + ":31001"
	stagenetRunStamp = "20060102-150405"
	// artifactsDirName holds the artifacts of stagenet runs, in the e2e module.
	artifactsDirName = "artifacts"
	// oramaBinRel is the CLI under test, relative to the repository root.
	oramaBinRel = "core/bin/orama"
)

// addressPattern is an EVM address.
var addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// keyscanner returns the host keys ip's sshd offers; tests replace it.
type keyscanner func(ctx context.Context, ip string) ([]ssh.PublicKey, error)

// targetInput is everything `target stagenet` reads from the machine.
type targetInput struct {
	lay      layout
	realHome string
	out      string
	chainID  string
	now      time.Time
	scan     keyscanner
}

// cmdTarget writes the state file of a target: `target stagenet --out <state.json>`.
func cmdTarget(parent context.Context, args []string) (int, error) {
	if len(args) == 0 || args[0] != config.TargetStagenet {
		return exitUsage, errors.Join(errUsage, fmt.Errorf("expected: target %s --out <state.json> [--chain-id ID]", config.TargetStagenet))
	}
	fs := flag.NewFlagSet("target "+config.TargetStagenet, flag.ContinueOnError)
	out := fs.String("out", "", "path of the state file to write")
	chainID := fs.String("chain-id", "", "the stagenet chain id; read from the running chain when omitted, and must match it when given")
	if err := parseFlags(fs, args[1:]); err != nil {
		return exitUsage, err
	}
	if *out == "" || fs.NArg() != 0 {
		return exitUsage, errors.Join(errUsage, errors.New("expected: target stagenet --out <state.json> [--chain-id ID]"))
	}
	lay, err := findLayout()
	if err != nil {
		return exitFail, err
	}
	realHome, err := secrets.RealHome()
	if err != nil {
		return exitFail, err
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return exitFail, fmt.Errorf("failed to resolve --out %s: %w", *out, err)
	}
	ctx, cancel := context.WithTimeout(parent, keyscanTimeout*time.Duration(len(config.StagenetNodes)))
	defer cancel()
	id, err := resolveChainID(ctx, *chainID, gatewayChainID(config.StagenetPath(realHome, config.StagenetCAFileRel)))
	if err != nil {
		return exitFail, err
	}
	st, err := writeStagenetState(ctx, targetInput{lay: lay, realHome: realHome, out: abs, chainID: id, now: time.Now().UTC(), scan: sshKeyscan})
	if err != nil {
		return exitFail, err
	}
	if _, err := os.Stat(st.OramaBin); err != nil {
		fmt.Fprintf(os.Stderr, "e2e-fleet: warning: the CLI under test %s does not exist yet (build core/bin/orama before `test`)\n", st.OramaBin)
	}
	fmt.Printf("E2E_FLEET_STATE=%s\n", abs)
	return exitOK, nil
}

// writeStagenetState builds the stagenet state, applies the run guards to it
// and writes it (and its pinned known_hosts) to in.out.
func writeStagenetState(ctx context.Context, in targetInput) (*fleet.State, error) {
	if err := config.CheckStagenetChainID(in.chainID); err != nil {
		return nil, err
	}
	hosts := strings.TrimSuffix(in.out, filepath.Ext(in.out)) + ".known_hosts"
	if err := pinStagenetHostKeys(ctx, in, hosts); err != nil {
		return nil, err
	}
	addr, err := readOperatorAddress(config.StagenetPath(in.realHome, config.StagenetRWReadyRel))
	if err != nil {
		return nil, err
	}
	runID := "stagenet-" + in.now.Format(stagenetRunStamp)
	st := &fleet.State{
		Target: config.TargetStagenet, RunID: runID, Env: config.StagenetEnv,
		BaseDomain: config.StagenetBaseDomain, GatewayURL: config.StagenetGatewayURL,
		CAFile:   config.StagenetPath(in.realHome, config.StagenetCAFileRel),
		OramaBin: filepath.Join(in.lay.repo, oramaBinRel), Home: config.StagenetPath(in.realHome, config.StagenetHomeRel),
		RWSock: config.StagenetPath(in.realHome, config.StagenetRWSockRel), OperatorAddress: addr,
		OperatorNamespace: config.StagenetOperatorNamespace,
		SSHKeyFile:        config.StagenetPath(in.realHome, config.StagenetSSHKeyRel), KnownHostsFile: hosts,
		ChainID: in.chainID, ChainRPC: stagenetRPCURL,
		ArtifactDir: filepath.Join(in.lay.module, artifactsDirName, runID),
	}
	for _, n := range config.StagenetNodes {
		role := fleet.RoleNode
		if n.Nameserver {
			role = fleet.RoleNameserver
		}
		st.Nodes = append(st.Nodes, fleet.Node{Name: n.Name, Role: role, PublicIP: n.IP, WGIP: n.WGIP, SSHUser: n.User})
	}
	if err := fleet.CheckState(st, in.realHome); err != nil {
		return nil, fmt.Errorf("the stagenet state fails the run guards: %w", err)
	}
	if err := st.Save(in.out); err != nil {
		return nil, err
	}
	return st, nil
}

// readOperatorAddress is the "address" of the dev RootWallet agent's ready file.
func readOperatorAddress(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read the dev RootWallet agent's ready file (is it running? start ~/orama-stagenet-handoff/rwdev.sh): %w", err)
	}
	var ready struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &ready); err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if !addressPattern.MatchString(ready.Address) {
		return "", fmt.Errorf("%s holds address %q, which is not an EVM address", path, ready.Address)
	}
	return ready.Address, nil
}

// pinStagenetHostKeys scans the stagenet nodes' host keys and writes to hosts
// only the keys the owner's own known_hosts also holds for that address. A
// scanned key that differs from a key of the same type in the owner's file
// is refused (a changed host key), and so is a node the owner's file has no
// key of: the scan alone would trust whatever answered.
func pinStagenetHostKeys(ctx context.Context, in targetInput, hosts string) error {
	userFile := config.StagenetPath(in.realHome, config.StagenetKnownHostsRel)
	check, err := knownhosts.New(userFile)
	if err != nil {
		return fmt.Errorf("failed to read %s to cross-check the host keys: %w", userFile, err)
	}
	if err := os.Remove(hosts); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to replace %s: %w", hosts, err)
	}
	for _, n := range config.StagenetNodes {
		keys, err := in.scan(ctx, n.IP)
		if err != nil {
			return fmt.Errorf("failed to scan the host keys of %s (%s): %w", n.Label, n.IP, err)
		}
		pinned, err := crossCheck(check, n, keys)
		if err != nil {
			return err
		}
		for _, k := range pinned {
			if err := sshx.AppendKnownHost(hosts, n.IP, k); err != nil {
				return err
			}
		}
	}
	return nil
}

// crossCheck returns the scanned keys of n that the owner's known_hosts holds.
func crossCheck(check ssh.HostKeyCallback, n config.StagenetNode, scanned []ssh.PublicKey) ([]ssh.PublicKey, error) {
	remote := &net.TCPAddr{IP: net.ParseIP(n.IP), Port: sshPort}
	host := net.JoinHostPort(n.IP, fmt.Sprint(sshPort))
	var pinned []ssh.PublicKey
	for _, key := range scanned {
		err := check(host, remote, key)
		var ke *knownhosts.KeyError
		switch {
		case err == nil:
			pinned = append(pinned, key)
		case errors.As(err, &ke) && len(ke.Want) == 0:
			return nil, fmt.Errorf("%s (%s) is not in your known_hosts: connect to it once with `ssh` and verify its fingerprint, then re-run", n.Label, n.IP)
		case errors.As(err, &ke):
			for _, w := range ke.Want {
				if w.Key.Type() == key.Type() {
					return nil, fmt.Errorf("HOST KEY MISMATCH for %s (%s): the %s key it offers (%s) is not the one in your known_hosts (%s); refusing",
						n.Label, n.IP, key.Type(), sshx.Fingerprint(key), sshx.Fingerprint(w.Key))
				}
			}
		default:
			return nil, fmt.Errorf("failed to cross-check the %s key of %s against your known_hosts: %w", key.Type(), n.IP, err)
		}
	}
	if len(pinned) == 0 {
		return nil, fmt.Errorf("none of the host keys %s (%s) offers is in your known_hosts: connect once with `ssh` and verify its fingerprint, then re-run", n.Label, n.IP)
	}
	return pinned, nil
}

// sshKeyscan runs ssh-keyscan against ip and parses the keys it prints.
func sshKeyscan(ctx context.Context, ip string) ([]ssh.PublicKey, error) {
	out, err := exec.CommandContext(ctx, "ssh-keyscan", "-T", fmt.Sprint(int(keyscanTimeout.Seconds())), "-t", keyscanTypes, ip).Output()
	if err != nil {
		return nil, fmt.Errorf("ssh-keyscan: %w", err)
	}
	return parseKeyscan(out)
}

// parseKeyscan reads ssh-keyscan's known_hosts-format output.
func parseKeyscan(out []byte) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for rest := out; len(rest) > 0; {
		_, _, key, _, next, err := ssh.ParseKnownHosts(rest)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse the ssh-keyscan output: %w", err)
		}
		keys = append(keys, key)
		rest = next
	}
	if len(keys) == 0 {
		return nil, errors.New("ssh-keyscan returned no host key")
	}
	return keys, nil
}
