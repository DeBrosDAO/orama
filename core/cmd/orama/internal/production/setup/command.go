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
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// Options holds the flags for the setup command.
type Options struct {
	IP         string
	Env        string
	Role       string // "node" or "nameserver"
	User       string // SSH user (default: "root")
	Password   string // One-time password for initial SSH access
	BaseDomain string
	Gateway    string // Gateway URL to use for invite tokens (overrides env config)
	Genesis    bool   // If true, create a new cluster instead of joining
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
	addrData, err := agentClient.GetAddress(ctx, "evm")
	if err != nil {
		return fmt.Errorf("failed to get wallet address: %w", err)
	}
	fmt.Printf("  Wallet: %s\n", addrData.Address)

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
	if err := ensureArchive(node, opts.Archive); err != nil {
		return err
	}

	// 7. Build the install command
	installCmd, err := buildInstallCommand(opts, node, agentClient)
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

// enrollKey installs pubKey on the VPS over the operator's existing
// credential: the VPS password (--password) or a private key that already
// opens it (--bootstrap-key). With neither, the key must already be there.
//
// The credential is only used after the host key is pinned: this connection
// bootstraps every later trust relationship with the node, so accepting an
// unknown key here would hand the credential to whoever answered.
func enrollKey(opts Options, pubKey, knownHosts string) error {
	if opts.Password != "" && opts.BootstrapKey != "" {
		return fmt.Errorf("--password and --bootstrap-key are alternatives; pass one")
	}
	if opts.Password == "" && opts.BootstrapKey == "" {
		fmt.Println("  No --password or --bootstrap-key, assuming the RootWallet key is already installed")
		return nil
	}

	fmt.Printf("  Installing SSH key on %s...\n", opts.IP)
	var err error
	if opts.Password != "" {
		err = installPublicKey(opts.IP, opts.User, opts.Password, pubKey, knownHosts)
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
		if opts.Password == "" && opts.BootstrapKey == "" {
			return fmt.Errorf("the RootWallet key for %s@%s does not open the VPS (%s); "+
				"install it once with --password (password login) or --bootstrap-key <private key that opens the VPS today>",
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

// buildInstallCommand constructs the `sudo orama node install` command.
func buildInstallCommand(opts Options, node inspector.Node, agentClient *rwagent.Client) (string, error) {
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

	// Get wallet address for operator tagging
	ctx := context.Background()
	if addrData, err := agentClient.GetAddress(ctx, "evm"); err == nil && addrData.Address != "" {
		flag("--operator-wallet", addrData.Address)
	}

	if !opts.Genesis {
		joinArgs, err := joinArguments(opts)
		if err != nil {
			return "", err
		}
		for i := 0; i+1 < len(joinArgs); i += 2 {
			flag(joinArgs[i], joinArgs[i+1])
		}
	}

	return strings.Join(parts, " "), nil
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inviteFormat is what `orama node invite` prints — the orama1_ prefix and
// base64url — or a bare 64-hex token from an older cluster. The invite
// travels into a root command on the new VPS, so nothing else is accepted.
var inviteFormat = regexp.MustCompile(`^(orama1_[A-Za-z0-9_-]+|[0-9a-f]{64})$`)

// validateInvite checks an invite received from another machine.
func validateInvite(token string) error {
	if !inviteFormat.MatchString(token) {
		return fmt.Errorf("invite has an unexpected form")
	}
	if _, err := invite.Decode(token); err != nil {
		return fmt.Errorf("invite does not decode: %w", err)
	}
	return nil
}

// joinArguments returns the install flags that join the cluster: an invite
// minted on an existing node over SSH (--join-via), or one requested from the
// gateway's operator API.
func joinArguments(opts Options) ([]string, error) {
	if opts.JoinVia != "" {
		if opts.Gateway != "" {
			return nil, fmt.Errorf("--join-via and --gateway are alternatives; pass one")
		}
		token, err := mintInviteOverSSH(opts.JoinVia)
		if err != nil {
			return nil, err
		}
		// The invite carries the gateway to join and the certificate to pin.
		return []string{"--token", token}, nil
	}

	gatewayURL := opts.Gateway
	if gatewayURL == "" {
		env := opts.Env
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return nil, fmt.Errorf("failed to get active environment: %w", err)
			}
			env = active.Name
		}
		envConfig, err := cli.GetEnvironmentByName(env)
		if err != nil {
			return nil, fmt.Errorf("environment %q not found (use --join-via or --gateway): %w", env, err)
		}
		gatewayURL = envConfig.GatewayURL
	}

	token, err := requestInviteToken(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get invite token: %w", err)
	}
	return []string{"--join", gatewayURL, "--token", token}, nil
}

// mintInviteOverSSH runs `orama node invite --raw` on an existing node,
// reached with its RootWallet key, and returns the invite it prints.
func mintInviteOverSSH(joinVia string) (string, error) {
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

	res := inspector.RunSSH(context.Background(), via[0], "sudo -n /opt/orama/bin/orama node invite --raw --expiry "+inviteExpiry.String())
	if !res.OK() {
		return "", fmt.Errorf("mint an invite on %s: %s", joinVia, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	token := strings.TrimSpace(res.Stdout)
	if err := validateInvite(token); err != nil {
		return "", fmt.Errorf("mint an invite on %s: %w", joinVia, err)
	}
	return token, nil
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

// requestInviteToken calls POST /v1/operator/invite to get an invite token.
func requestInviteToken(gatewayURL string) (string, error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", fmt.Errorf("no credentials for %s — run 'orama auth login' first", gatewayURL)
	}
	token, err := auth.Bearer(gatewayURL, store, creds)
	if err != nil {
		return "", err
	}

	body, _ := json.Marshal(map[string]int{"expiry_minutes": 60})
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/operator/invite", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if err := validateInvite(result.Token); err != nil {
		return "", fmt.Errorf("invite from %s: %w", gatewayURL, err)
	}
	return result.Token, nil
}

// ensureArchive makes the node run the build in archivePath (or the newest
// archive when it is empty). It compares the archive's manifest with the one
// installed on the node rather than asking whether any orama binary exists:
// that check let a re-run of setup install whatever older build the node
// already had.
func ensureArchive(node inspector.Node, archivePath string) error {
	if archivePath == "" {
		archivePath = build.FindNewestArchive()
		if archivePath == "" {
			return fmt.Errorf("no binary archive in %s (run `orama build`, or pass --archive)", build.ArchiveDir)
		}
	}
	want, err := build.ReadArchiveManifest(archivePath)
	if err != nil {
		return err
	}
	have := inspector.RunSSH(context.Background(), node, "cat /opt/orama/"+build.ManifestName)
	if have.OK() && bytes.Equal(bytes.TrimSpace([]byte(have.Stdout)), bytes.TrimSpace(want)) {
		fmt.Printf("  Node already runs this build (%s)\n", filepath.Base(archivePath))
		return nil
	}

	// A fresh private directory, not a fixed /tmp path that root then extracts:
	// another local user could have planted or swapped a file there.
	dir, err := remotessh.RunSSHOutput(node, "mktemp -d /tmp/orama-archive.XXXXXXXX")
	if err != nil {
		return fmt.Errorf("create an upload directory on the node: %w", err)
	}
	dir = strings.TrimSpace(dir)
	if !uploadDirPattern.MatchString(dir) {
		return fmt.Errorf("unexpected upload directory %q from mktemp", dir)
	}

	fmt.Printf("  Uploading archive (%s)...\n", archivePath)
	if err := remotessh.UploadFile(node, archivePath, dir+"/archive.tar.gz"); err != nil {
		return fmt.Errorf("failed to upload archive: %w", err)
	}
	if err := remotessh.RunSSHStreaming(node, extractArchiveCommand(dir)); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}
	fmt.Println("  Archive extracted")
	return nil
}

// uploadDirPattern is what `mktemp -d /tmp/orama-archive.XXXXXXXX` returns; the
// directory is interpolated into a root shell command, so nothing else passes.
var uploadDirPattern = regexp.MustCompile(`^/tmp/orama-archive\.[A-Za-z0-9]{8}$`)

// extractArchiveCommand replaces the archive-owned entries under /opt/orama
// with the archive uploaded to dir, leaving the node's data (.orama/)
// untouched, and removes dir. It unpacks into a staging directory first, so a
// corrupt archive fails before anything installed is removed, and the window
// in which a restarting service could find its binary gone is one copy long.
func extractArchiveCommand(dir string) string {
	var rm []string
	for _, p := range build.ArchiveOwnedPaths {
		rm = append(rm, "/opt/orama/"+p)
	}
	stage := dir + "/extract"
	return "sudo bash -c 'set -e; trap \"rm -rf " + dir + "\" EXIT; mkdir -p /opt/orama " + stage + "; tar xzf " + dir + "/archive.tar.gz -C " + stage +
		"; rm -rf " + strings.Join(rm, " ") + "; cp -a " + stage + "/. /opt/orama/; rm -rf " + dir + "'"
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
