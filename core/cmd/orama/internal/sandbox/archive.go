package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// A sandbox installs and rolls out builds exactly as a production cluster does
// (docs/SECURITY.md, "Signed build archives"): create puts the archive on each
// fresh server with node setup's path — verified here against the operator's
// wallet, uploaded as the canonical re-pack, staged by `node stage-archive`,
// which creates the trust anchor — and rollout uses push's path, which each
// node verifies against that anchor. These are those shared functions;
// variables so tests can observe what the sandbox hands them.
var (
	ensureArchives = setup.EnsureArchives
	pushArchive    = push.ToNodes
	scanHostKeys   = setup.ScanHostKeys
)

// readOperatorWallet is the operator's RootWallet address: the account
// `orama build` signs with, and the only signer the sandbox trusts.
var readOperatorWallet = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentAddressTimeout)
	defer cancel()
	return setup.OperatorWallet(ctx, rwagent.New(os.Getenv("RW_AGENT_SOCK")))
}

const (
	// agentAddressTimeout bounds asking the RootWallet agent for its address.
	agentAddressTimeout = 10 * time.Second
	// sandboxACMECA is where sandbox nodes get certificates: Let's Encrypt
	// staging, whose limits a cluster rebuilt several times a week stays
	// within (production allows five certificates a week for the same names).
	// The CLI trusts its roots for the sandbox domain only (stagingroots.go).
	sandboxACMECA = "letsencrypt-staging"
	// sshReadyTimeout is how long a freshly booted server has to answer SSH.
	sshReadyTimeout = 5 * time.Minute
	// knownHostsSuffix names a sandbox's pinned host keys, next to its state.
	knownHostsSuffix = ".known_hosts"
	// sandboxUser is the SSH user on every sandbox server.
	sandboxUser = "root"
	// sandboxEnvironment is the environment every sandbox node registers
	// under, and the name `orama` knows the active sandbox by.
	sandboxEnvironment = "sandbox"
	// certificateTimeout is how long genesis has to obtain its certificate.
	certificateTimeout = 5 * time.Minute
	// readinessPollInterval spaces the readiness probes of a new node.
	readinessPollInterval = 5 * time.Second
	// httpsPort is where a node's Caddy serves TLS.
	httpsPort = "443"
)

// knownHostsPath is the sandbox's own known_hosts file.
func knownHostsPath(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	dir, err := sandboxesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+knownHostsSuffix), nil
}

// pinHostKeys records the SSH host keys every server presents on first
// contact (trust on first use), straight after it was created and before any
// command, archive or invite is sent to it, in the sandbox's own known_hosts.
// Every SSH connection create and rollout make afterwards is checked against
// them. Hetzner recycles addresses, so the operator's known_hosts is the wrong
// place for them; and with no check at all, whatever answered at an address
// would be handed the invite.
//
// A server is ready once sshd answers the scan: cloud-init writes the host
// keys before sshd starts, so the first keys it presents are its keys. Each
// server has timeout to answer.
func pinHostKeys(state *SandboxState, timeout time.Duration) error {
	path, err := knownHostsPath(state.Name)
	if err != nil {
		return err
	}
	var lines []string
	for _, srv := range state.Servers {
		keys, err := scanWhenReady(srv.IP, timeout)
		if err != nil {
			return fmt.Errorf("scan the SSH host key of %s: %w", srv.Name, err)
		}
		lines = append(lines, keys...)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write the sandbox's known_hosts %s: %w", path, err)
	}
	return nil
}

// scanWhenReady scans ip's host keys, polling until its sshd answers.
func scanWhenReady(ip string, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	for {
		keys, err := scanHostKeys(ip)
		if err == nil {
			return keys, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no SSH host key after %s: %w", timeout, err)
		}
		time.Sleep(readinessPollInterval)
	}
}

// pinnedNodes are the sandbox's servers as SSH targets: the sandbox key, and
// only the host keys pinned when the sandbox was created.
func pinnedNodes(state *SandboxState, sshKeyPath string) ([]inspector.Node, error) {
	path, err := knownHostsPath(state.Name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("sandbox %q has no pinned SSH host keys (%w): it was created before sandboxes "+
			"pinned them and verified archives; destroy it and create a new one", state.Name, err)
	}
	nodes := make([]inspector.Node, len(state.Servers))
	for i, srv := range state.Servers {
		nodes[i] = inspector.Node{
			Environment:    sandboxEnvironment,
			User:           sandboxUser,
			Host:           srv.IP,
			Role:           srv.Role,
			SSHKey:         sshKeyPath,
			KnownHostsFile: path,
		}
	}
	return nodes, nil
}

// installArchive puts the archive on every pinned node through node setup's
// verified path, trusting only wallet.
func installArchive(nodes []inspector.Node, archivePath, wallet string) error {
	return ensureArchives(nodes, archivePath, []string{wallet})
}

// installOptions describes srv to node setup's install command line.
func installOptions(cfg *Config, srv ServerState, genesis bool) setup.Options {
	return setup.Options{
		IP: srv.IP, User: sandboxUser, Role: srv.Role, BaseDomain: cfg.Domain,
		Env: sandboxEnvironment, ACMECA: sandboxACMECA, Genesis: genesis,
	}
}

// genesisInstallCommand installs the first server: a new cluster whose
// archive trust anchor is the operator's wallet. --skip-checks because
// sandbox servers are smaller than a production node's minimum.
func genesisInstallCommand(cfg *Config, srv ServerState, wallet string) string {
	return setup.InstallCommand(installOptions(cfg, srv, true), wallet, nil, "") + " --skip-checks"
}

// joinInstallCommand joins srv to the cluster with invite, expecting the
// cluster to send the operator's wallet as its only archive signer.
func joinInstallCommand(cfg *Config, srv ServerState, wallet, invite string) string {
	return setup.InstallCommand(installOptions(cfg, srv, false), wallet, []string{wallet}, invite) + " --skip-checks"
}

// mintInvite mints a single-use invite on the genesis node the way node setup
// does over --join-via. It names that node and the certificate it serves, so
// the joining node pins both.
func mintInvite(genesis inspector.Node) (string, error) {
	out, err := remotessh.RunSSHOutput(genesis, setup.MintInviteCommand())
	if err != nil {
		return "", fmt.Errorf("mint an invite on %s: %w", genesis.Host, err)
	}
	token, err := setup.ParseMintedInvite(out)
	if err != nil {
		return "", fmt.Errorf("mint an invite on %s: %w", genesis.Host, err)
	}
	return token, nil
}

// waitForCertificate polls until addr serves a TLS certificate for domain:
// what `orama node invite` fingerprints for the joining node to pin. It reads
// the certificate without verifying it — a certificate that is not trusted
// yet is still pinnable.
func waitForCertificate(addr, domain string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := invite.FingerprintServed(addr, domain)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no certificate for %s at %s after %s: %w", domain, addr, timeout, err)
		}
		time.Sleep(readinessPollInterval)
	}
}

// pushToSandbox rolls the archive out to the pinned nodes through push, which
// each node stages with its installed orama against its trust anchor.
func pushToSandbox(nodes []inspector.Node, archivePath string) error {
	return pushArchive(archivePath, nodes, false, nil)
}
