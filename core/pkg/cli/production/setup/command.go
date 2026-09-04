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
	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
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

	// 4. Install the public key on the VPS via password SSH.
	//
	// The password is only sent after the host key is pinned: this connection
	// bootstraps every later trust relationship with the node, so accepting an
	// unknown key here would hand the password to whoever answered.
	if opts.Password != "" {
		fmt.Printf("  Scanning SSH host key for %s...\n", opts.IP)
		hk, err := scanHostKey(opts.IP)
		if err != nil {
			return err
		}
		if err := confirmHostKey(hk, opts.IP, opts.HostKey, os.Stdin, os.Stdout); err != nil {
			return err
		}

		khDir, err := os.MkdirTemp("", "orama-setup-")
		if err != nil {
			return fmt.Errorf("create temp dir for known_hosts: %w", err)
		}
		defer os.RemoveAll(khDir)

		knownHosts, err := hk.writeKnownHosts(khDir)
		if err != nil {
			return err
		}

		fmt.Printf("  Installing SSH key on %s...\n", opts.IP)
		if err := installPublicKey(opts.IP, opts.User, opts.Password, pubKey, knownHosts); err != nil {
			return fmt.Errorf("failed to install SSH key: %w", err)
		}
		fmt.Println("  SSH key installed")
	} else {
		fmt.Println("  No --password provided, assuming SSH key is already installed")
	}

	// 5. Test SSH with rootwallet key
	fmt.Println("  Testing SSH connection...")
	node := inspector.Node{
		Host:        opts.IP,
		User:        opts.User,
		VaultTarget: vaultTarget,
		Environment: opts.Env,
		Role:        opts.Role,
	}
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key: %w", err)
	}
	defer cleanup()
	node = nodes[0] // SSHKey is now set

	testResult := inspector.RunSSH(context.Background(), node, "echo ok")
	if !testResult.OK() {
		return fmt.Errorf("SSH test failed: %s", testResult.Stderr)
	}
	fmt.Println("  SSH connection OK")

	// 6. Check if binary archive needs uploading
	if needsArchiveUpload(node) {
		archivePath := build.FindNewestArchive()
		if archivePath == "" {
			return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
		}
		fmt.Printf("  Uploading archive (%s)...\n", filepath.Base(archivePath))
		if err := remotessh.UploadFile(node, archivePath, "/tmp/archive.tar.gz"); err != nil {
			return fmt.Errorf("failed to upload archive: %w", err)
		}
		extractCmd := "sudo bash -c 'mkdir -p /opt/orama && tar xzf /tmp/archive.tar.gz -C /opt/orama && rm -f /tmp/archive.tar.gz'"
		if err := remotessh.RunSSHStreaming(node, extractCmd); err != nil {
			return fmt.Errorf("failed to extract archive: %w", err)
		}
		fmt.Println("  Archive extracted")
	} else {
		fmt.Println("  Binary already present on node")
	}

	// 7. Build the install command
	installCmd, err := buildInstallCommand(opts, node, agentClient)
	if err != nil {
		return fmt.Errorf("failed to build install command: %w", err)
	}

	fmt.Printf("\n  Running: %s\n\n", installCmd)

	// 8. Run the install
	if err := remotessh.RunSSHStreaming(node, installCmd); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	// 9. After genesis install, update the environment gateway URL to this node's IP.
	// This allows subsequent `node setup` calls to find the gateway automatically.
	if opts.Genesis && opts.Env != "" {
		gatewayURL := fmt.Sprintf("http://%s", opts.IP)
		desc := fmt.Sprintf("%s (genesis: %s)", opts.Env, opts.IP)
		if err := cli.AddEnvironment(opts.Env, gatewayURL, desc); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to update environment: %v\n", err)
		} else {
			if err := cli.SwitchEnvironment(opts.Env); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to switch environment: %v\n", err)
			}
			fmt.Printf("  Environment %q updated: gateway → %s\n", opts.Env, gatewayURL)
			fmt.Printf("\n  To join more nodes, first authenticate:\n")
			fmt.Printf("    orama auth login\n")
			fmt.Printf("  Then:\n")
			fmt.Printf("    orama node setup --ip <IP> --password '<PASS>' --env %s --base-domain %s\n", opts.Env, opts.BaseDomain)
		}
	}

	fmt.Printf("\n  Node %s setup complete!\n", opts.IP)
	return nil
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
	parts = append(parts, "--vps-ip", opts.IP)
	parts = append(parts, "--anyone-client")

	if opts.BaseDomain != "" {
		parts = append(parts, "--base-domain", opts.BaseDomain)
	}

	if strings.HasPrefix(opts.Role, "nameserver") {
		parts = append(parts, "--nameserver")
		if opts.BaseDomain != "" {
			parts = append(parts, "--domain", opts.BaseDomain)
		}
	}

	// Pass operator metadata so the node registers with correct values
	if opts.User != "" {
		parts = append(parts, "--ssh-user", opts.User)
	}
	if opts.Env != "" {
		parts = append(parts, "--environment", opts.Env)
	}

	// Get wallet address for operator tagging
	ctx := context.Background()
	if addrData, err := agentClient.GetAddress(ctx, "evm"); err == nil && addrData.Address != "" {
		parts = append(parts, "--operator-wallet", addrData.Address)
	}

	if !opts.Genesis {
		// Determine gateway URL for invite token request
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
				return "", fmt.Errorf("environment %q not found (use --gateway to specify directly): %w", env, err)
			}
			gatewayURL = envConfig.GatewayURL
		}

		// Request invite token via operator API
		token, err := requestInviteToken(gatewayURL)
		if err != nil {
			return "", fmt.Errorf("failed to get invite token: %w", err)
		}

		parts = append(parts, "--join", gatewayURL, "--token", token)
	}

	return strings.Join(parts, " "), nil
}

// requestInviteToken calls POST /v1/operator/invite to get an invite token.
func requestInviteToken(gatewayURL string) (string, error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || creds.APIKey == "" {
		return "", fmt.Errorf("no credentials for %s — run 'orama auth login' first", gatewayURL)
	}

	body, _ := json.Marshal(map[string]int{"expiry_minutes": 60})
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/operator/invite", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", creds.APIKey)

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
	if result.Token == "" {
		return "", fmt.Errorf("empty token in response")
	}
	return result.Token, nil
}

// needsArchiveUpload checks if the node already has the orama binary.
func needsArchiveUpload(node inspector.Node) bool {
	result := inspector.RunSSH(context.Background(), node, "/opt/orama/bin/orama version 2>/dev/null")
	return !result.OK()
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
