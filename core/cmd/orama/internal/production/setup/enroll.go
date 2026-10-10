package setup

import (
	"fmt"

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
