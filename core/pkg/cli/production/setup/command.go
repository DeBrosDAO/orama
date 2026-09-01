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
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

	// 4. Install the public key on the VPS via password SSH
	if opts.Password != "" {
		fmt.Printf("  Installing SSH key on %s...\n", opts.IP)
		if err := installPublicKey(opts.IP, opts.User, opts.Password, pubKey); err != nil {
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
		archivePath := findNewestArchive()
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

// installPublicKey installs an SSH public key on a VPS using password authentication.
func installPublicKey(ip, user, password, pubKey string) error {
	sshpassBin, err := findBinary("sshpass")
	if err != nil {
		return fmt.Errorf("sshpass is required for password-based SSH key installation: %w", err)
	}

	// Ensure .ssh directory exists and install the key
	cmd := fmt.Sprintf(
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && echo 'key installed'`,
		strings.TrimSpace(pubKey),
	)

	args := []string{
		"-p", password,
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "PreferredAuthentications=password",
		"-o", "PubkeyAuthentication=no",
		fmt.Sprintf("%s@%s", user, ip),
		cmd,
	}

	out, err := runCommand(sshpassBin, args...)
	if err != nil {
		return fmt.Errorf("sshpass failed: %w (%s)", err, out)
	}
	if !strings.Contains(out, "key installed") {
		return fmt.Errorf("unexpected output: %s", out)
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

// findNewestArchive finds the newest orama binary archive in /tmp/.
func findNewestArchive() string {
	matches, _ := filepath.Glob("/tmp/orama-*-linux-*.tar.gz")
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0]
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
