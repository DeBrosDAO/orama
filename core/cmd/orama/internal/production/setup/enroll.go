package setup

import (
	"fmt"
	"net"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// EnrollRequest is one VPS to reach: the RootWallet vault gets an SSH key for
// it, the VPS's host key is pinned, and the public key is installed with the
// credential the operator has today (the VPS password, or a private key).
type EnrollRequest struct {
	IP   string
	User string
	// UsePassword enrols over password login. The password is the vault login
	// entry for the IP, or Password.
	UsePassword bool
	// Password is a password typed for this run; it is never stored.
	Password string
	// BootstrapKey is a private key that already opens the VPS.
	BootstrapKey string
	// HostKey is the expected SHA256:... fingerprint; empty asks.
	HostKey string
	Role    string
	Env     string
}

// Enrolled is a VPS setup can run commands on.
type Enrolled struct {
	// Node has its SSHKey and KnownHostsFile set: every connection of the run
	// goes to the host key that was pinned.
	Node inspector.Node
	// Close removes the temporary key and known_hosts files.
	Close func()
}

// Enroll gives the operator's RootWallet an SSH key on the VPS and proves it
// opens it. It is the first half of `orama node setup`, which Run now calls,
// and the first step of `orama setup` for each of its IPs.
func Enroll(req EnrollRequest) (*Enrolled, error) {
	user := req.User
	if user == "" {
		user = "root"
	}
	opts := Options{
		IP: req.IP, User: user, UsePassword: req.UsePassword, Password: req.Password,
		BootstrapKey: req.BootstrapKey, HostKey: req.HostKey, Role: req.Role, Env: req.Env,
	}
	vaultTarget := fmt.Sprintf("%s/%s", req.IP, user)
	fmt.Printf("  Setting up SSH key for %s...\n", vaultTarget)
	if err := remotessh.EnsureVaultEntry(vaultTarget); err != nil {
		return nil, fmt.Errorf("failed to create SSH key in vault: %w", err)
	}
	pubKey, err := remotessh.ResolveVaultPublicKey(vaultTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	// The pin holds for every connection of the run, not only the enrollment
	// one: the archive upload, the root extract and the install carrying the
	// invite all go to the host the operator verified.
	knownHosts, unpin, err := pinHostKey(opts)
	if err != nil {
		return nil, err
	}
	if err := enrollKey(opts, pubKey, knownHosts); err != nil {
		unpin()
		return nil, err
	}
	fmt.Println("  Testing SSH connection...")
	nodes := []inspector.Node{{
		Host: req.IP, User: user, VaultTarget: vaultTarget, Environment: req.Env, Role: req.Role, KnownHostsFile: knownHosts,
	}}
	cleanupKeys, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		unpin()
		return nil, fmt.Errorf("failed to prepare SSH key: %w", err)
	}
	if err := checkNodeAccess(opts, nodes[0]); err != nil {
		cleanupKeys()
		unpin()
		return nil, err
	}
	fmt.Println("  SSH connection OK")
	return &Enrolled{Node: nodes[0], Close: func() { cleanupKeys(); unpin() }}, nil
}

// NodeArch is the architecture the node reports, as Go names it (amd64, arm64).
func NodeArch(node inspector.Node) (string, error) {
	return nodeArchitecture(node)
}

// Reach opens a machine that was enrolled before: the RootWallet already has a
// key on it and its host key is in ~/.orama/known_hosts. Nothing is installed
// on it and nothing is asked; a host key that is not in that file is a refusal,
// never a first contact (the same rule as --join-via).
func Reach(ip, user string) (*Enrolled, error) {
	knownHosts, err := operatorKnownHosts()
	if err != nil {
		return nil, err
	}
	node, err := reachableNode(ip, user, knownHosts)
	if err != nil {
		return nil, err
	}
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return nil, fmt.Errorf("the RootWallet has no SSH key for %s@%s: %w", user, ip, err)
	}
	if err := checkNodeAccess(Options{IP: ip, User: user}, nodes[0]); err != nil {
		cleanup()
		return nil, err
	}
	return &Enrolled{Node: nodes[0], Close: cleanup}, nil
}

// reachableNode is the machine Reach opens: its login and address are checked
// (they come from the CLI's configuration, and go into an ssh command line), and
// it is held to the known_hosts file with no other source of host keys.
func reachableNode(ip, user, knownHosts string) (inspector.Node, error) {
	if user == "" {
		user = "root"
	}
	if !sshUserPattern.MatchString(user) {
		return inspector.Node{}, fmt.Errorf("the recorded login %q is not a login name", user)
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return inspector.Node{}, fmt.Errorf("the recorded address %q is not an IPv4 address", ip)
	}
	return inspector.Node{Host: ip, User: user, VaultTarget: ip + "/" + user, KnownHostsFile: knownHosts}, nil
}

// HostKeyInfo is one SSH host key a machine presents: its type and its
// SHA256:... fingerprint, as a provider's console shows it.
type HostKeyInfo struct {
	Type        string
	Fingerprint string
}

// HostKeys scans the host keys ip presents now, with their fingerprints. Nothing
// vouches for them: the caller shows them to the operator, who compares them with
// the provider's console, and passes the one that matched as the expected
// fingerprint of EnrollRequest.
func HostKeys(ip string) ([]HostKeyInfo, error) {
	hk, err := scanHostKey(ip)
	if err != nil {
		return nil, err
	}
	return hostKeyInfos(hk), nil
}

// hostKeyInfos describes each scanned key: the type is the second field of its
// known_hosts line (ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa).
func hostKeyInfos(hk *hostKey) []HostKeyInfo {
	keys := make([]HostKeyInfo, len(hk.lines))
	for i, line := range hk.lines {
		fields := strings.Fields(line)
		typ := "unknown"
		if len(fields) >= 2 {
			typ = strings.TrimPrefix(fields[1], "ssh-")
		}
		keys[i] = HostKeyInfo{Type: typ, Fingerprint: hk.fingerprints[i]}
	}
	return keys
}
