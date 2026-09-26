// Package setup implements the "orama node setup" command — a single command
// to bootstrap a fresh VPS into a running Orama node.
//
// Flow:
//  1. Create SSH key in rootwallet vault for this node
//  2. Install the public key on the VPS (one-time password-based SSH)
//  3. Upload the binary archive
//  4. For genesis: run install without --join
//  5. For joining: request invite token via operator API, run install with --join
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/invitemint"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// Options holds the flags for the setup command.
type Options struct {
	IP   string
	Env  string
	Role string // "node" or "nameserver"
	User string // SSH user (default: "root")
	// UsePassword bootstraps over password login. The password comes from the
	// operator's RootWallet vault login entry for the IP, never the command
	// line, where ps and shell history would keep the credential that owns
	// the machine.
	UsePassword bool
	BaseDomain  string
	Gateway     string // Gateway URL (the cluster domain) to mint the invite through (overrides env config)
	Genesis     bool   // If true, create a new cluster instead of joining
	// HostKey pins the VPS's expected SSH host-key fingerprint (SHA256:...) so
	// enrollment can run unattended. Empty means confirm it interactively.
	HostKey string
	// BootstrapKey is an SSH private key that already opens the VPS, used
	// once to install the RootWallet-managed key. It is the path for
	// providers that ship key-only images with a non-root sudo user. The key
	// is read by ssh only; it is never copied or stored.
	BootstrapKey string
	// Archive is the build archive to install. Empty means the newest one in
	// build.ArchiveDir.
	Archive string
	// ACMECA is passed to `orama node install --acme-ca`.
	ACMECA string
	// JoinVia is "user@ip" of a node already in the cluster. The invite is
	// minted on it over SSH with its RootWallet key, instead of through the
	// gateway's operator API — no `orama auth login` and no bearer token.
	JoinVia string
}

// Run executes the node setup.
func Run(opts Options) error {
	if opts.IP == "" {
		return fmt.Errorf("--ip is required")
	}
	if opts.User == "" {
		opts.User = "root"
	}
	if opts.Role == "" {
		opts.Role = "node"
	}

	// 1. Ensure rootwallet agent is running
	fmt.Println("Checking rootwallet agent...")
	agentClient := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := agentClient.Status(ctx)
	if err != nil {
		return fmt.Errorf("rootwallet agent not reachable: %w (is the desktop app running?)", err)
	}
	if status.Locked {
		return fmt.Errorf("rootwallet agent is locked — unlock it in the desktop app first")
	}

	// 2. Get operator wallet address
	operator, err := OperatorWallet(ctx, agentClient)
	if err != nil {
		return err
	}
	wallet := []string{operator}
	fmt.Printf("  Wallet: %s\n", operator)

	// 3. Create SSH key in rootwallet vault for this node
	vaultTarget := fmt.Sprintf("%s/%s", opts.IP, opts.User)
	fmt.Printf("  Setting up SSH key for %s...\n", vaultTarget)

	if err := remotessh.EnsureVaultEntry(vaultTarget); err != nil {
		return fmt.Errorf("failed to create SSH key in vault: %w", err)
	}

	pubKey, err := remotessh.ResolveVaultPublicKey(vaultTarget)
	if err != nil {
		return fmt.Errorf("failed to get public key: %w", err)
	}

	// 4. Pin the VPS host key, then install the public key with the
	// operator's existing credential, if one was given. The pin holds for
	// every connection this run makes, not only the enrollment one: the
	// archive upload, the root extract and the install carrying the invite all
	// go to the host the operator verified.
	knownHosts, unpin, err := pinHostKey(opts)
	if err != nil {
		return err
	}
	defer unpin()
	if err := enrollKey(opts, pubKey, knownHosts); err != nil {
		return err
	}

	// 5. Test SSH with rootwallet key
	fmt.Println("  Testing SSH connection...")
	node := inspector.Node{
		Host:           opts.IP,
		User:           opts.User,
		VaultTarget:    vaultTarget,
		Environment:    opts.Env,
		Role:           opts.Role,
		KnownHostsFile: knownHosts,
	}
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key: %w", err)
	}
	defer cleanup()
	node = nodes[0] // SSHKey is now set

	if err := checkNodeAccess(opts, node); err != nil {
		return err
	}
	fmt.Println("  SSH connection OK")

	// 6. Put exactly this build on the node.
	if err := EnsureArchive(node, opts.Archive, wallet); err != nil {
		return err
	}

	// 7. Build the install command
	expected, err := expectedArchiveSigners(opts, wallet)
	if err != nil {
		return err
	}
	installCmd, err := buildInstallCommand(opts, wallet[0], expected)
	if err != nil {
		return fmt.Errorf("failed to build install command: %w", err)
	}

	fmt.Printf("\n  Running: %s\n\n", redactToken(installCmd))

	// 8. Run the install
	if err := remotessh.RunSSHStreaming(node, installCmd); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	// 9. After genesis, record the environment so later commands can name it
	// with --env. The active environment is left alone: switching it here
	// silently redirected every later command on a machine that also operates
	// other clusters.
	if opts.Genesis && opts.Env != "" {
		if err := recordEnvironment(opts); err != nil {
			return err
		}
	}

	fmt.Printf("\n  Node %s setup complete!\n", opts.IP)
	return nil
}

// OperatorWallet is the operator's RootWallet address, normalized: the account
// `orama build` signs archives with, which a genesis node makes its archive
// trust anchor and every node registers under.
func OperatorWallet(ctx context.Context, agent *rwagent.Client) (string, error) {
	addrData, err := agent.GetAddress(ctx, archiveSigningChain)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet address: %w", err)
	}
	wallet, err := archivetrust.NormalizeSigners([]string{addrData.Address})
	if err != nil {
		return "", fmt.Errorf("the RootWallet agent's address: %w", err)
	}
	return wallet[0], nil
}

// archiveSigningChain is the RootWallet chain whose account signs archives.
const archiveSigningChain = "evm"

// vaultPasswordTimeout bounds the vault lookup; the agent may prompt to unlock.
const vaultPasswordTimeout = 2 * time.Minute

// vaultPassword reads the VPS login password from the operator's RootWallet
// vault: the login entry whose site is the IP, for user. A variable so tests
// need no agent.
var vaultPassword = func(ip, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), vaultPasswordTimeout)
	defer cancel()
	data, err := rwagent.New(os.Getenv("RW_AGENT_SOCK")).GetPassword(ctx, ip, user)
	if rwagent.IsNotFound(err) {
		return "", fmt.Errorf("--password: your RootWallet vault has no login for %s with user %s; "+
			"store it with `rw vault add %s` (username %s) and run setup again", ip, user, ip, user)
	}
	if err != nil {
		return "", fmt.Errorf("--password: read the login for %s@%s from RootWallet: %w", user, ip, err)
	}
	if data.Password == "" {
		return "", fmt.Errorf("--password: the RootWallet login for %s@%s has an empty password", user, ip)
	}
	return data.Password, nil
}

// enrollKey installs pubKey on the VPS over the operator's existing
// credential: the VPS password (--password) or a private key that already
// opens it (--bootstrap-key). With neither, the key must already be there.
//
// The credential is only used after the host key is pinned: this connection
// bootstraps every later trust relationship with the node, so accepting an
// unknown key here would hand the credential to whoever answered.
func enrollKey(opts Options, pubKey, knownHosts string) error {
	if opts.UsePassword && opts.BootstrapKey != "" {
		return fmt.Errorf("--password and --bootstrap-key are alternatives; pass one")
	}
	if !opts.UsePassword && opts.BootstrapKey == "" {
		fmt.Println("  No --password or --bootstrap-key, assuming the RootWallet key is already installed")
		return nil
	}

	fmt.Printf("  Installing SSH key on %s...\n", opts.IP)
	var err error
	if opts.UsePassword {
		var password string
		if password, err = vaultPassword(opts.IP, opts.User); err != nil {
			return err
		}
		err = installPublicKey(opts.IP, opts.User, password, pubKey, knownHosts)
	} else {
		err = installPublicKeyWithKey(opts.IP, opts.User, opts.BootstrapKey, pubKey, knownHosts)
	}
	if err != nil {
		return fmt.Errorf("failed to install SSH key: %w", err)
	}
	fmt.Println("  SSH key installed")
	return nil
}

// pinHostKey scans the VPS host key, has the operator confirm it (or matches
// --host-key), and writes the trusted entries to a private known_hosts file
// that every connection of this run uses. The returned func removes it.
func pinHostKey(opts Options) (string, func(), error) {
	fmt.Printf("  Scanning SSH host key for %s...\n", opts.IP)
	hk, err := scanHostKey(opts.IP)
	if err != nil {
		return "", nil, err
	}
	trusted, err := confirmHostKey(hk, opts.IP, opts.HostKey, os.Stdin, os.Stdout)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "orama-setup-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir for known_hosts: %w", err)
	}
	path, err := writeKnownHosts(dir, trusted)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

// checkNodeAccess proves the RootWallet key opens the VPS and, for a non-root
// user, that sudo works without a password — the install runs under sudo on
// a connection with no terminal to type one into.
func checkNodeAccess(opts Options, node inspector.Node) error {
	res := inspector.RunSSH(context.Background(), node, "echo ok")
	if !res.OK() {
		if !opts.UsePassword && opts.BootstrapKey == "" {
			return fmt.Errorf("the RootWallet key for %s@%s does not open the VPS (%s); "+
				"install it once with --password (password login, read from your RootWallet vault) or --bootstrap-key <private key that opens the VPS today>",
				opts.User, opts.IP, strings.TrimSpace(res.Stderr))
		}
		return fmt.Errorf("SSH with the RootWallet key failed right after installing it: %s", strings.TrimSpace(res.Stderr))
	}
	if opts.User == "root" {
		return nil
	}
	if res := inspector.RunSSH(context.Background(), node, "sudo -n true"); !res.OK() {
		return fmt.Errorf("user %s on %s needs passwordless sudo: the install runs sudo non-interactively (%s)",
			opts.User, opts.IP, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// installKeyWithKeyArgs builds the ssh argument list for enrollment over an
// existing private key. Only that key is offered (IdentitiesOnly), the
// operator's ssh config cannot swap in another host key policy, and the pinned
// known_hosts is the only one consulted.
func installKeyWithKeyArgs(ip, user, keyPath, knownHostsPath string) []string {
	return []string{
		"-F", "/dev/null", // the operator's ssh config must not override the pin
		"-i", keyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + knownHostsPath,
		"-o", "ConnectTimeout=10",
		"-o", "PreferredAuthentications=publickey",
		fmt.Sprintf("%s@%s", user, ip),
		installKeyScript,
	}
}

// installPublicKeyWithKey installs pubKey using a private key that already
// opens the VPS, against a host key the operator has pinned.
func installPublicKeyWithKey(ip, user, keyPath, pubKey, knownHostsPath string) error {
	if _, err := os.Stat(keyPath); err != nil {
		return fmt.Errorf("--bootstrap-key %s: %w", keyPath, err)
	}
	sshBin, err := findBinary("ssh")
	if err != nil {
		return fmt.Errorf("ssh is required for key-based enrollment: %w", err)
	}

	out, err := runCommandWithEnvStdin(sshBin, nil, strings.TrimSpace(pubKey)+"\n",
		installKeyWithKeyArgs(ip, user, keyPath, knownHostsPath)...)
	if err != nil {
		return fmt.Errorf("installing the SSH key with --bootstrap-key failed: %w (%s)", err, strings.TrimSpace(out))
	}
	if !strings.Contains(out, "key installed") {
		return fmt.Errorf("the VPS did not confirm the key was installed: %s", strings.TrimSpace(out))
	}
	return nil
}

// recordEnvironment adds (or updates) the environment for a new cluster. Its
// gateway is https://<base domain>: that is the name the cluster serves once
// its nameservers are delegated, and the only URL an operator token should
// travel to.
func recordEnvironment(opts Options) error {
	if opts.BaseDomain == "" {
		return fmt.Errorf("genesis needs --base-domain to record environment %q", opts.Env)
	}
	gatewayURL := "https://" + opts.BaseDomain
	desc := fmt.Sprintf("%s (genesis %s)", opts.Env, opts.IP)
	if err := cli.AddEnvironment(opts.Env, gatewayURL, desc); err != nil {
		return fmt.Errorf("record environment %q: %w", opts.Env, err)
	}
	fmt.Printf("  Environment %q recorded: gateway %s (the active environment is unchanged)\n", opts.Env, gatewayURL)
	fmt.Printf("\n  Next: delegate %s to this cluster's nameservers (see docs/NAMESERVER_SETUP.md),\n", opts.BaseDomain)
	fmt.Printf("  then join more nodes through this one:\n")
	fmt.Printf("    orama node setup --ip <IP> --user <user> --env %s --base-domain %s --join-via %s@%s\n",
		opts.Env, opts.BaseDomain, opts.User, opts.IP)
	return nil
}

// redactToken hides the invite in a printed install command: it is a
// single-use credential to join the cluster and does not belong in a terminal
// scrollback or a CI log.
func redactToken(cmd string) string {
	fields := strings.Fields(cmd)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "--token" {
			fields[i+1] = "'<invite>'"
		}
	}
	return strings.Join(fields, " ")
}

// installKeyScript appends the public key to authorized_keys unless it is
// already there.
//
// The key arrives on stdin rather than interpolated into this script, so it
// needs no shell quoting and cannot terminate the command it travels in. Each
// step is checked (set -e) and the dedupe is an explicit if, because chaining
// the append with `||` would also run it when an earlier step failed.
const installKeyScript = `set -e
mkdir -p ~/.ssh
chmod 700 ~/.ssh
touch ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
key=$(cat)
if ! grep -qxF "$key" ~/.ssh/authorized_keys; then
  printf '%s\n' "$key" >> ~/.ssh/authorized_keys
fi
echo 'key installed'`

// installKeyArgs builds the sshpass argument list for the enrollment
// connection. It carries no secret: the password travels in SSHPASS, which
// "-e" tells sshpass to read.
func installKeyArgs(ip, user, knownHostsPath string) []string {
	return []string{
		"-e", // read the password from SSHPASS, never from argv
		"ssh",
		"-F", "/dev/null", // the operator's ssh config must not override the pin
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + knownHostsPath,
		"-o", "ConnectTimeout=10",
		"-o", "PreferredAuthentications=password",
		"-o", "PubkeyAuthentication=no",
		fmt.Sprintf("%s@%s", user, ip),
		installKeyScript,
	}
}

// installPublicKey installs an SSH public key on a VPS using password
// authentication, against a host key the operator has already pinned.
//
// The password is handed to sshpass through the environment, never argv: an
// argument is visible to every local process in ps, and this is the one
// credential that can hand over the whole machine. Host-key checking stays on
// and points at knownHostsPath, so the password is only ever sent to the host
// the operator confirmed.
func installPublicKey(ip, user, password, pubKey, knownHostsPath string) error {
	sshpassBin, err := findBinary("sshpass")
	if err != nil {
		return fmt.Errorf("sshpass is required for password-based SSH key installation: %w", err)
	}

	args := installKeyArgs(ip, user, knownHostsPath)

	out, err := runCommandWithEnvStdin(
		sshpassBin,
		[]string{"SSHPASS=" + password},
		strings.TrimSpace(pubKey)+"\n",
		args...,
	)
	if err != nil {
		return fmt.Errorf("installing the SSH key over password authentication failed: %w (%s)", err, strings.TrimSpace(out))
	}
	if !strings.Contains(out, "key installed") {
		return fmt.Errorf("the VPS did not confirm the key was installed: %s", strings.TrimSpace(out))
	}
	return nil
}

// buildInstallCommand constructs the `sudo orama node install` command,
// minting the invite a joining node needs.
func buildInstallCommand(opts Options, wallet string, expected []string) (string, error) {
	var token string
	if !opts.Genesis {
		var err error
		if token, err = joinInvite(opts); err != nil {
			return "", err
		}
	}
	return InstallCommand(opts, wallet, expected, token), nil
}

// InstallCommand is the `sudo orama node install` command line for opts.
// wallet is the operator's RootWallet address: the node registers under it,
// and a genesis node makes it the archive trust anchor.
// expected is the archive signer list a joining node must receive and token
// the invite it joins with; both are unused for a genesis node.
func InstallCommand(opts Options, wallet string, expected []string, token string) string {
	parts := []string{"sudo /opt/orama/bin/orama node install"}
	// Every value is single-quoted: this line runs as root on the VPS, and the
	// invite in it comes from another machine.
	flag := func(name, value string) { parts = append(parts, name, shellQuote(value)) }
	flag("--vps-ip", opts.IP)

	if opts.BaseDomain != "" {
		flag("--base-domain", opts.BaseDomain)
	}

	if strings.HasPrefix(opts.Role, "nameserver") {
		parts = append(parts, "--nameserver")
		if opts.BaseDomain != "" {
			flag("--domain", opts.BaseDomain)
		}
	}

	// Pass operator metadata so the node registers with correct values
	if opts.User != "" {
		flag("--ssh-user", opts.User)
	}
	if opts.Env != "" {
		flag("--environment", opts.Env)
	}
	if opts.ACMECA != "" {
		flag("--acme-ca", opts.ACMECA)
	}

	flag("--operator-wallet", wallet)

	if !opts.Genesis {
		flag("--expect-archive-signers", strings.Join(expected, ","))
		// The invite carries the gateway to join and the certificate to pin.
		flag("--token", token)
	}

	return strings.Join(parts, " ")
}

// expectedArchiveSigners is the archive signer list a joining node must
// receive in the join response: the anchor of the --join-via node, read over
// SSH, or else the operator's own wallet — the one setup verified the archive
// against. A cluster that trusts more signers than that is joined with
// --join-via.
func expectedArchiveSigners(opts Options, wallet []string) ([]string, error) {
	switch {
	case opts.Genesis:
		return nil, nil
	case opts.JoinVia != "":
		return clusterArchiveSigners(opts.JoinVia)
	default:
		return wallet, nil
	}
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inviteFormat is what `orama node invite` prints — the orama1_ prefix and
// base64url — or a bare 64-hex token from an older cluster. The invite
// travels into a root command on the new VPS, so nothing else is accepted.
var inviteFormat = regexp.MustCompile(`^(orama1_[A-Za-z0-9_-]+|[0-9a-f]{64})$`)

// validateInvite checks an invite received from another machine: it goes into
// a root command on the new node.
func validateInvite(token string) error {
	if !inviteFormat.MatchString(token) {
		return fmt.Errorf("invite has an unexpected form")
	}
	if _, err := invite.Decode(token); err != nil {
		return fmt.Errorf("invite does not decode: %w", err)
	}
	return nil
}

// joinInvite returns the invite that joins the cluster: one minted on an
// existing node over SSH (--join-via), or one requested from the gateway's
// operator API.
func joinInvite(opts Options) (string, error) {
	if opts.JoinVia != "" {
		if opts.Gateway != "" {
			return "", fmt.Errorf("--join-via and --gateway are alternatives; pass one")
		}
		return mintInviteOverSSH(opts.JoinVia)
	}

	gatewayURL := opts.Gateway
	if gatewayURL == "" {
		env := opts.Env
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return "", fmt.Errorf("failed to get active environment: %w", err)
			}
			env = active.Name
		}
		envConfig, err := cli.GetEnvironmentByName(env)
		if err != nil {
			return "", fmt.Errorf("environment %q not found (use --join-via or --gateway): %w", env, err)
		}
		gatewayURL = envConfig.GatewayURL
	}

	return mintInviteThroughGateway(gatewayURL)
}

// MintInviteCommand is the command that, run on a node already in the
// cluster, mints a single-use invite naming that node and the certificate it
// serves, and prints only the invite.
func MintInviteCommand() string {
	return "sudo -n /opt/orama/bin/orama node invite --raw --expiry " + inviteExpiry.String()
}

// ParseMintedInvite is the invite in the output of MintInviteCommand, checked:
// it goes into a root command on the new node.
func ParseMintedInvite(out string) (string, error) {
	token := strings.TrimSpace(out)
	if err := validateInvite(token); err != nil {
		return "", err
	}
	return token, nil
}

// mintInviteOverSSH runs MintInviteCommand on an existing node, reached with
// its RootWallet key, and returns the invite it prints.
func mintInviteOverSSH(joinVia string) (string, error) {
	res, err := runOnJoinVia(joinVia, MintInviteCommand())
	if err != nil {
		return "", fmt.Errorf("mint an invite on %s: %w", joinVia, err)
	}
	token, err := ParseMintedInvite(res)
	if err != nil {
		return "", fmt.Errorf("mint an invite on %s: %w", joinVia, err)
	}
	return token, nil
}

// clusterArchiveSigners reads the archive trust anchor of the node the invite
// is minted on, over the operator's own SSH to it: the list a joining node
// expects the join response to carry. It is root-owned on that node, unlike
// the gateway that serves the response.
func clusterArchiveSigners(joinVia string) ([]string, error) {
	out, err := runOnJoinVia(joinVia, "cat "+archivetrust.AnchorPath)
	if err != nil {
		return nil, fmt.Errorf("read the archive trust anchor on %s: %w", joinVia, err)
	}
	signers, err := archivetrust.ParseAnchor([]byte(out))
	if err != nil {
		return nil, fmt.Errorf("the archive trust anchor on %s: %w", joinVia, err)
	}
	return signers, nil
}

// runOnJoinVia runs cmd on the --join-via node and returns its output.
func runOnJoinVia(joinVia, cmd string) (string, error) {
	user, host, ok := strings.Cut(joinVia, "@")
	if !ok || !sshUserPattern.MatchString(user) || net.ParseIP(host) == nil {
		return "", fmt.Errorf("--join-via %q: want user@ip of a node already in the cluster", joinVia)
	}
	knownHosts, err := operatorKnownHosts()
	if err != nil {
		return "", err
	}
	// An existing cluster node is one the operator has reached before; a first
	// contact here is an error, not something to accept.
	via := []inspector.Node{{Host: host, User: user, VaultTarget: host + "/" + user, KnownHostsFile: knownHosts}}
	cleanup, err := remotessh.PrepareNodeKeys(via)
	if err != nil {
		return "", fmt.Errorf("--join-via %s: %w", joinVia, err)
	}
	defer cleanup()
	res := inspector.RunSSH(context.Background(), via[0], cmd)
	if !res.OK() {
		return "", fmt.Errorf("%s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	return res.Stdout, nil
}

// sshUserPattern is a POSIX login name; nothing that ssh could read as an
// option.
var sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// operatorKnownHosts is the operator's own known_hosts file.
func operatorKnownHosts() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for known_hosts: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// inviteExpiry bounds an invite minted for one setup run; the install that
// consumes it starts seconds later.
const inviteExpiry = 15 * time.Minute

// mintInviteThroughGateway mints an invite through the gateway's operator API
// that names one node of the cluster and pins the certificate it served.
//
// It used to hand install `--join <gateway> --token <token>`: the cluster's
// domain, which reaches whichever nameserver DNS picks, and no fingerprint, so
// the joining node trusted the first certificate it was shown. The invite is
// now minted the way `orama invite` mints it (pkg invitemint).
func mintInviteThroughGateway(gatewayURL string) (string, error) {
	host, err := invitemint.GatewayHost(gatewayURL)
	if err != nil {
		return "", err
	}
	nodeIP, err := invitemint.ChooseNode(context.Background(), "", host)
	if err != nil {
		return "", fmt.Errorf("%w (or mint the invite on a node you choose with --join-via user@ip)", err)
	}
	bearer, err := shared.AuthToken(gatewayURL)
	if err != nil {
		return "", err
	}
	m, err := invitemint.MintThrough(gatewayURL, host, nodeIP, bearer, inviteExpiry)
	if err != nil {
		return "", fmt.Errorf("%w (or mint the invite on another node with --join-via user@ip)", err)
	}
	return m.Invite, nil
}

// EnsureArchive makes the node run the build in archivePath, verified against
// trusted before anything reaches the node.
//
// A new machine has no verified orama of its own: the binary that runs the
// install comes out of this archive, so the archive is verified here, on the
// operator's machine, and what is uploaded is a canonical archive written from
// the verified tree (archivetrust.PrepareUpload) — never the file the operator
// named. On the node the CLI is extracted alone, checked against the verified
// manifest's checksum, and put the archive in place with `node stage-archive`:
// the same verification, lock and crash-safe swap a push uses.
//
// It is skipped only when the node already has exactly this manifest and a CLI
// matching its checksum; comparing manifests alone let a replaced binary run.
func EnsureArchive(node inspector.Node, archivePath string, trusted []string) error {
	return EnsureArchives([]inspector.Node{node}, archivePath, trusted)
}

// EnsureArchives is EnsureArchive for several nodes: the archive is verified
// and re-packed once, then put on each node in turn, stopping at the first
// node that fails.
func EnsureArchives(nodes []inspector.Node, archivePath string, trusted []string) (err error) {
	if archivePath == "" {
		// /tmp is shared: its newest archive can be another checkout's build.
		return fmt.Errorf("--archive is required: the path `orama build` printed")
	}
	upload, err := archivetrust.PrepareUpload(archivePath, trusted)
	if err != nil {
		return fmt.Errorf("refusing to upload %s: %w", archivePath, err)
	}
	defer func() { err = errors.Join(err, upload.Remove()) }()
	m := upload.Verified.Manifest
	fmt.Printf("  Archive verified: v%s linux/%s signed by %s\n", m.Version, m.Arch, upload.Verified.Signer)

	// Verification compared it with a computed digest, case-insensitively;
	// sha256sum prints lowercase.
	cliSum := strings.ToLower(m.Checksums[archiveCLIName])
	if cliSum == "" {
		return fmt.Errorf("the verified archive has no %s binary to install with", archiveCLIName)
	}
	for _, node := range nodes {
		if err := ensureVerifiedArchive(node, upload.Path, m.Arch, cliSum, trusted); err != nil {
			return fmt.Errorf("node %s: %w", node.Host, err)
		}
	}
	return nil
}

// ensureVerifiedArchive puts the verified, re-packed archive on node unless it
// already runs exactly that build.
func ensureVerifiedArchive(node inspector.Node, archive, arch, cliSum string, trusted []string) error {
	if err := checkNodeArch(node, arch); err != nil {
		return err
	}
	current, err := nodeRunsBuild(node, archive, cliSum)
	if err != nil {
		return err
	}
	if current {
		fmt.Printf("  %s already runs this build\n", node.Host)
		return nil
	}
	return uploadAndStage(node, archive, cliSum, trusted)
}

// archiveCLIName is the orama CLI in an archive's bin/.
const archiveCLIName = "orama"

// nodeRunsBuild reports whether the node's /opt/orama holds exactly the
// manifest in archive and a CLI with the checksum it lists.
func nodeRunsBuild(node inspector.Node, archive, cliSum string) (bool, error) {
	want, err := build.ReadArchiveManifest(archive)
	if err != nil {
		return false, err
	}
	have := inspector.RunSSH(context.Background(), node, "cat /opt/orama/"+build.ManifestName)
	if !have.OK() || !bytes.Equal(bytes.TrimSpace([]byte(have.Stdout)), bytes.TrimSpace(want)) {
		return false, nil
	}
	sum := inspector.RunSSH(context.Background(), node, "sha256sum /opt/orama/bin/"+archiveCLIName)
	got, _, _ := strings.Cut(strings.TrimSpace(sum.Stdout), " ")
	return sum.OK() && got == cliSum, nil
}

// goArch names the architectures `uname -m` reports as Go does.
var goArch = map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}

// checkNodeArch refuses an archive built for another architecture than the
// node's, before anything is uploaded — for a join, before the invite is spent.
func checkNodeArch(node inspector.Node, arch string) error {
	out, err := remotessh.RunSSHOutput(node, "uname -m")
	if err != nil {
		return fmt.Errorf("read the node's architecture: %w", err)
	}
	machine := strings.TrimSpace(out)
	nodeArch, ok := goArch[machine]
	if !ok {
		return fmt.Errorf("the node reports architecture %q, which no build targets", machine)
	}
	if nodeArch != arch {
		return fmt.Errorf("the archive is built for linux/%s and the node is linux/%s: build with --arch %s", arch, nodeArch, nodeArch)
	}
	return nil
}

// uploadAndStage uploads the verified archive into a private directory on the
// node and stages it there. The directory is removed however that ends.
func uploadAndStage(node inspector.Node, archive, cliSum string, trusted []string) error {
	dir, err := remotessh.RunSSHOutput(node, "mktemp -d /tmp/orama-archive.XXXXXXXX")
	if err != nil {
		return fmt.Errorf("create an upload directory on the node: %w", err)
	}
	dir = strings.TrimSpace(dir)
	if !uploadDirPattern.MatchString(dir) {
		return fmt.Errorf("unexpected upload directory %q from mktemp", dir)
	}
	fmt.Printf("  Uploading the verified archive...\n")
	if err := remotessh.UploadFile(node, archive, dir+"/archive.tar.gz"); err != nil {
		rmErr := remotessh.RunSSHStreaming(node, "rm -rf "+dir)
		return errors.Join(fmt.Errorf("failed to upload archive: %w", err), rmErr)
	}
	if err := remotessh.RunSSHStreaming(node, stageArchiveCommand(dir, cliSum, trusted)); err != nil {
		return fmt.Errorf("failed to stage the archive on the node: %w", err)
	}
	fmt.Println("  Archive verified and in place on the node")
	return nil
}

// uploadDirPattern is what `mktemp -d /tmp/orama-archive.XXXXXXXX` returns; the
// directory is interpolated into a root shell command, so nothing else passes.
var uploadDirPattern = regexp.MustCompile(`^/tmp/orama-archive\.[A-Za-z0-9]{8}$`)

// stageArchiveCommand, as root on the node: extracts only the CLI from the
// archive uploaded to dir into a new root-only directory under /opt/orama (not
// /tmp, which may be noexec), refuses it unless it has the checksum the
// verified manifest lists, and runs its `node stage-archive` on the archive —
// which verifies it again against the node's anchor, takes the archive lock
// and swaps it in with rollback. A node without an anchor gets one from
// trusted, written only after the archive verified against it. Both
// directories are always removed.
//
// Every value interpolated is safe in a shell: dir matches uploadDirPattern,
// cliSum is a checksum from a verified manifest (hex — verification compared
// it with a computed digest — lowercased by EnsureArchive), and trusted are
// normalized addresses.
func stageArchiveCommand(dir, cliSum string, trusted []string) string {
	archive := dir + "/archive.tar.gz"
	cli := "/opt/orama/" + push.SetupCLIPrefix + strings.TrimPrefix(dir, "/tmp/orama-archive.")
	stage := cli + "/bin/orama node stage-archive --archive " + archive
	// /opt/orama must be root's alone before a binary is run from under it:
	// anyone else who could write it could swap the checked CLI.
	// A find that cannot inspect it prints nothing, which must not pass.
	rootOnly := "bad=$(find /opt/orama -maxdepth 0 \\( ! -user root -o -perm -020 -o -perm -002 \\) -print) || " +
		"{ echo \"cannot inspect /opt/orama\" >&2; exit 1; }; " +
		"[ -z \"$bad\" ] || { echo \"/opt/orama is not owned by root and writable only by root\" >&2; exit 1; }; "
	// The archive decides what bin/orama is: a symlink would make the checksum,
	// and the run after it, follow it anywhere.
	regularCLI := "[ -f " + cli + "/bin/orama ] && [ ! -L " + cli + "/bin/orama ] || " +
		"{ echo \"bin/orama in the archive is not a regular file\" >&2; exit 1; }; "
	return "sudo bash -c 'set -e; trap \"rm -rf " + dir + " " + cli + "\" EXIT; " +
		"mkdir -p /opt/orama; " + rootOnly + "mkdir -m 700 " + cli + "; " +
		"tar --no-same-owner -xzf " + archive + " -C " + cli + " bin/orama; " + regularCLI +
		"echo \"" + cliSum + "  " + cli + "/bin/orama\" | sha256sum -c --quiet -; " +
		"if [ -e " + archivetrust.AnchorPath + " ]; then " + stage + "; " +
		"else " + stage + " --trust-signers " + strings.Join(trusted, ",") + "; fi'"
}

func findBinary(name string) (string, error) {
	paths := []string{
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
		"/usr/bin/" + name,
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

func runCommand(bin string, args ...string) (string, error) {
	cmd := &exec.Cmd{
		Path: bin,
		Args: append([]string{bin}, args...),
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
