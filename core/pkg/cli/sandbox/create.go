package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// Create orchestrates the creation of a new sandbox cluster.
func Create(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	// --- Preflight: validate everything BEFORE spending money ---
	fmt.Println("Preflight checks:")

	// 1. Check for existing active sandbox
	active, err := FindActiveSandbox()
	if err != nil {
		return err
	}
	if active != nil {
		return fmt.Errorf("sandbox %q is already active (status: %s)\nDestroy it first: orama sandbox destroy --name %s",
			active.Name, active.Status, active.Name)
	}
	fmt.Println("  [ok] No active sandbox")

	// 2. Check rootwallet agent is running and unlocked before the slow SSH key call
	if err := checkAgentReady(); err != nil {
		return err
	}
	fmt.Println("  [ok] Rootwallet agent running and unlocked")

	// 3. Resolve SSH key (may trigger approval prompt in RootWallet app)
	fmt.Print("  [..] Resolving SSH key from vault...")
	sshKeyPath, cleanup, err := resolveVaultKeyOnce(cfg.SSHKey.VaultTarget)
	if err != nil {
		fmt.Println(" FAILED")
		return fmt.Errorf("prepare SSH key: %w", err)
	}
	defer cleanup()
	fmt.Println(" ok")

	// 4. Check binary archive — auto-build if missing
	archivePath := findNewestArchive()
	if archivePath == "" {
		fmt.Println("  [--] No binary archive found, building...")
		if err := autoBuildArchive(); err != nil {
			return fmt.Errorf("auto-build archive: %w", err)
		}
		archivePath = findNewestArchive()
		if archivePath == "" {
			return fmt.Errorf("build succeeded but no archive found in /tmp/")
		}
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat archive %s: %w", archivePath, err)
	}
	fmt.Printf("  [ok] Binary archive: %s (%s)\n", filepath.Base(archivePath), formatBytes(info.Size()))

	// 5. Verify Hetzner API token works
	client := NewHetznerClient(cfg.HetznerAPIToken)
	if err := client.ValidateToken(); err != nil {
		return fmt.Errorf("hetzner API: %w\n     Check your token in ~/.orama/sandbox.yaml", err)
	}
	fmt.Println("  [ok] Hetzner API token valid")

	fmt.Println()

	// --- All preflight checks passed, proceed ---

	// Generate name if not provided
	if name == "" {
		name = GenerateName()
	}

	fmt.Printf("Creating sandbox %q (%s, %d nodes)\n\n", name, cfg.Domain, 5)

	state := &SandboxState{
		Name:      name,
		CreatedAt: time.Now().UTC(),
		Domain:    cfg.Domain,
		Status:    StatusCreating,
	}

	// Phase 1: Provision servers
	fmt.Println("Phase 1: Provisioning servers...")
	if err := phase1ProvisionServers(client, cfg, state); err != nil {
		cleanupFailedCreate(client, state)
		return fmt.Errorf("provision servers: %w", err)
	}
	if err := SaveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: save state after provisioning: %v\n", err)
	}

	// Phase 2: Assign floating IPs
	fmt.Println("\nPhase 2: Assigning floating IPs...")
	if err := phase2AssignFloatingIPs(client, cfg, state, sshKeyPath); err != nil {
		return fmt.Errorf("assign floating IPs: %w", err)
	}
	if err := SaveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: save state after floating IPs: %v\n", err)
	}

	// Phase 3: Upload binary archive
	fmt.Println("\nPhase 3: Uploading binary archive...")
	if err := phase3UploadArchive(state, sshKeyPath, archivePath); err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}

	// Phase 4: Install genesis node
	fmt.Println("\nPhase 4: Installing genesis node...")
	if err := phase4InstallGenesis(cfg, state, sshKeyPath); err != nil {
		state.Status = StatusError
		_ = SaveState(state)
		return fmt.Errorf("install genesis: %w", err)
	}

	// Phase 5: Join remaining nodes
	fmt.Println("\nPhase 5: Joining remaining nodes...")
	if err := phase5JoinNodes(cfg, state, sshKeyPath); err != nil {
		state.Status = StatusError
		_ = SaveState(state)
		return fmt.Errorf("join nodes: %w", err)
	}

	// Phase 6: Verify cluster
	fmt.Println("\nPhase 6: Verifying cluster...")
	phase6Verify(cfg, state, sshKeyPath)

	state.Status = StatusRunning
	if err := SaveState(state); err != nil {
		return fmt.Errorf("save final state: %w", err)
	}

	// Register sandbox as an environment and switch to it
	gatewayURL := "https://" + cfg.Domain
	desc := fmt.Sprintf("Sandbox cluster: %s (%s)", state.Name, cfg.Domain)
	if err := cli.AddEnvironment("sandbox", gatewayURL, desc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to register sandbox environment: %v\n", err)
	} else if err := cli.SwitchEnvironment("sandbox"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to switch to sandbox environment: %v\n", err)
	}

	// Tag all nodes with operator wallet for unified node management
	registerNodesWithOperator(state, sshKeyPath)

	printCreateSummary(cfg, state)
	return nil
}

// checkAgentReady verifies the rootwallet agent is running, unlocked, and
// that the desktop app is connected (required for first-time app approval).
func checkAgentReady() error {
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	status, err := client.Status(ctx)
	if err != nil {
		if rwagent.IsNotRunning(err) {
			return fmt.Errorf("rootwallet agent is not running\n\n  Start it with:\n    rw agent start && rw agent unlock")
		}
		return fmt.Errorf("rootwallet agent: %w", err)
	}

	return validateAgentStatus(status)
}

// validateAgentStatus checks that the agent status indicates readiness.
// Separated from checkAgentReady for testability.
func validateAgentStatus(status *rwagent.StatusResponse) error {
	if status.Locked {
		return fmt.Errorf("rootwallet agent is locked\n\n  Unlock it with:\n    rw agent unlock")
	}

	if status.ConnectedApps == 0 {
		fmt.Println("  [!!] RootWallet desktop app is not open")
		fmt.Println("       First-time use requires the desktop app to approve access.")
		fmt.Println("       Open the RootWallet app, then re-run this command.")
		return fmt.Errorf("RootWallet desktop app required for approval — open it and retry")
	}

	return nil
}

// resolveVaultKeyOnce resolves a wallet SSH key to a temp file.
// Returns the key path, cleanup function, and any error.
func resolveVaultKeyOnce(vaultTarget string) (string, func(), error) {
	node := inspector.Node{User: "root", Host: "resolve-only", VaultTarget: vaultTarget}
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return "", func() {}, err
	}
	return nodes[0].SSHKey, cleanup, nil
}

// phase1ProvisionServers creates 5 Hetzner servers in parallel.
func phase1ProvisionServers(client *HetznerClient, cfg *Config, state *SandboxState) error {
	type serverResult struct {
		index  int
		server *HetznerServer
		err    error
	}

	results := make(chan serverResult, 5)

	for i := 0; i < 5; i++ {
		go func(idx int) {
			role := "node"
			if idx < 2 {
				role = "nameserver"
			}

			serverName := fmt.Sprintf("sbx-%s-%d", state.Name, idx+1)
			labels := map[string]string{
				"orama-sandbox":      state.Name,
				"orama-sandbox-role": role,
			}

			req := CreateServerRequest{
				Name:       serverName,
				ServerType: cfg.ServerType,
				Image:      "ubuntu-24.04",
				Location:   cfg.Location,
				SSHKeys:    []int64{cfg.SSHKey.HetznerID},
				Labels:     labels,
			}
			if cfg.FirewallID > 0 {
				req.Firewalls = []struct {
					Firewall int64 `json:"firewall"`
				}{{Firewall: cfg.FirewallID}}
			}

			srv, err := client.CreateServer(req)
			results <- serverResult{index: idx, server: srv, err: err}
		}(i)
	}

	servers := make([]ServerState, 5)
	var firstErr error
	for i := 0; i < 5; i++ {
		r := <-results
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("server %d: %w", r.index+1, r.err)
			}
			continue
		}
		fmt.Printf("  Created %s (ID: %d, initializing...)\n", r.server.Name, r.server.ID)
		role := "node"
		if r.index < 2 {
			role = "nameserver"
		}
		servers[r.index] = ServerState{
			ID:   r.server.ID,
			Name: r.server.Name,
			Role: role,
		}
	}
	state.Servers = servers // populate before returning so cleanup can delete created servers
	if firstErr != nil {
		return firstErr
	}

	// Wait for all servers to reach "running"
	fmt.Print("  Waiting for servers to boot...")
	for i := range servers {
		srv, err := client.WaitForServer(servers[i].ID, 3*time.Minute)
		if err != nil {
			return fmt.Errorf("wait for %s: %w", servers[i].Name, err)
		}
		servers[i].IP = srv.PublicNet.IPv4.IP
		fmt.Print(".")
	}
	fmt.Println(" OK")

	// Assign floating IPs to nameserver entries
	if len(cfg.FloatingIPs) >= 2 {
		servers[0].FloatingIP = cfg.FloatingIPs[0].IP
		servers[1].FloatingIP = cfg.FloatingIPs[1].IP
	}

	state.Servers = servers

	for _, srv := range servers {
		fmt.Printf("  %s: %s (%s)\n", srv.Name, srv.IP, srv.Role)
	}

	return nil
}

// phase2AssignFloatingIPs assigns floating IPs and configures loopback.
func phase2AssignFloatingIPs(client *HetznerClient, cfg *Config, state *SandboxState, sshKeyPath string) error {
	for i := 0; i < 2 && i < len(cfg.FloatingIPs) && i < len(state.Servers); i++ {
		fip := cfg.FloatingIPs[i]
		srv := state.Servers[i]

		// Unassign if currently assigned elsewhere (ignore "not assigned" errors)
		fmt.Printf("  Assigning %s to %s...\n", fip.IP, srv.Name)
		if err := client.UnassignFloatingIP(fip.ID); err != nil {
			// Log but continue — may fail if not currently assigned, which is fine
			fmt.Printf("  Note: unassign %s: %v (continuing)\n", fip.IP, err)
		}

		if err := client.AssignFloatingIP(fip.ID, srv.ID); err != nil {
			return fmt.Errorf("assign %s to %s: %w", fip.IP, srv.Name, err)
		}

		// Configure floating IP on the server's loopback interface
		// Hetzner floating IPs require this: ip addr add <floating_ip>/32 dev lo
		node := inspector.Node{
			User:   "root",
			Host:   srv.IP,
			SSHKey: sshKeyPath,
		}

		// Wait for SSH to be ready on freshly booted servers
		if err := waitForSSH(node, 5*time.Minute); err != nil {
			return fmt.Errorf("SSH not ready on %s: %w", srv.Name, err)
		}

		cmd := fmt.Sprintf("ip addr add %s/32 dev lo 2>/dev/null || true", fip.IP)
		if err := remotessh.RunSSHStreaming(node, cmd, remotessh.WithNoHostKeyCheck()); err != nil {
			return fmt.Errorf("configure loopback on %s: %w", srv.Name, err)
		}
	}

	return nil
}

// waitForSSH polls until SSH is responsive on the node.
func waitForSSH(node inspector.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := runSSHOutput(node, "echo ok")
		if err == nil {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

// autoBuildArchive runs `make build-archive` from the project root.
func autoBuildArchive() error {
	// Find project root by looking for go.mod
	dir, err := findProjectRoot()
	if err != nil {
		return fmt.Errorf("find project root: %w", err)
	}

	cmd := exec.Command("make", "build-archive")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make build-archive failed: %w", err)
	}
	return nil
}

// findProjectRoot walks up from the current working directory to find go.mod.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod in any parent directory")
		}
		dir = parent
	}
}

// phase3UploadArchive uploads the binary archive to the genesis node, then fans out
// to the remaining nodes server-to-server (much faster than uploading from local machine).
func phase3UploadArchive(state *SandboxState, sshKeyPath, archivePath string) error {
	fmt.Printf("  Archive: %s\n", filepath.Base(archivePath))

	if err := fanoutArchive(state.Servers, sshKeyPath, archivePath); err != nil {
		return err
	}

	fmt.Println("  All nodes ready")
	return nil
}

// phase4InstallGenesis installs the genesis node.
func phase4InstallGenesis(cfg *Config, state *SandboxState, sshKeyPath string) error {
	genesis := state.GenesisServer()
	node := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}

	// Install genesis
	installCmd := fmt.Sprintf("/opt/orama/bin/orama node install --vps-ip %s --domain %s --base-domain %s --nameserver --anyone-client --skip-checks",
		genesis.IP, cfg.Domain, cfg.Domain)
	fmt.Printf("  Installing on %s (%s)...\n", genesis.Name, genesis.IP)
	if err := remotessh.RunSSHStreaming(node, installCmd, remotessh.WithNoHostKeyCheck()); err != nil {
		return fmt.Errorf("install genesis: %w", err)
	}

	// Wait for RQLite leader
	fmt.Print("  Waiting for RQLite leader...")
	if err := waitForRQLiteHealth(node, 3*time.Minute); err != nil {
		return fmt.Errorf("genesis health: %w", err)
	}
	fmt.Println(" OK")

	return nil
}

// phase5JoinNodes joins the remaining 4 nodes to the cluster (serial).
// Generates invite tokens just-in-time to avoid expiry during long installs.
func phase5JoinNodes(cfg *Config, state *SandboxState, sshKeyPath string) error {
	genesis := state.GenesisServer()
	genesisNode := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}

	for i := 1; i < len(state.Servers); i++ {
		srv := state.Servers[i]
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

		// Generate token just before use to avoid expiry
		token, err := generateInviteToken(genesisNode)
		if err != nil {
			return fmt.Errorf("generate invite token for %s: %w", srv.Name, err)
		}

		var installCmd string
		if srv.Role == "nameserver" {
			installCmd = fmt.Sprintf("/opt/orama/bin/orama node install --join http://%s --token %s --vps-ip %s --domain %s --base-domain %s --nameserver --anyone-client --skip-checks",
				genesis.IP, token, srv.IP, cfg.Domain, cfg.Domain)
		} else {
			installCmd = fmt.Sprintf("/opt/orama/bin/orama node install --join http://%s --token %s --vps-ip %s --base-domain %s --anyone-client --skip-checks",
				genesis.IP, token, srv.IP, cfg.Domain)
		}

		fmt.Printf("  [%d/%d] Joining %s (%s, %s)...\n", i, len(state.Servers)-1, srv.Name, srv.IP, srv.Role)
		if err := remotessh.RunSSHStreaming(node, installCmd, remotessh.WithNoHostKeyCheck()); err != nil {
			return fmt.Errorf("join %s: %w", srv.Name, err)
		}

		// Wait for node health before proceeding
		fmt.Printf("  Waiting for %s health...", srv.Name)
		if err := waitForRQLiteHealth(node, 3*time.Minute); err != nil {
			fmt.Printf(" WARN: %v\n", err)
		} else {
			fmt.Println(" OK")
		}
	}

	return nil
}

// phase6Verify runs a basic cluster health check.
func phase6Verify(cfg *Config, state *SandboxState, sshKeyPath string) {
	genesis := state.GenesisServer()
	node := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}

	// Check RQLite cluster
	out, err := runSSHOutput(node, "curl -s http://localhost:5001/status | grep -o '\"state\":\"[^\"]*\"' | head -1")
	if err == nil {
		fmt.Printf("  RQLite: %s\n", strings.TrimSpace(out))
	}

	// Check DNS (if floating IPs configured, only with safe domain names)
	if len(cfg.FloatingIPs) > 0 && isSafeDNSName(cfg.Domain) {
		out, err = runSSHOutput(node, fmt.Sprintf("dig +short @%s test.%s 2>/dev/null || echo 'DNS not responding'",
			cfg.FloatingIPs[0].IP, cfg.Domain))
		if err == nil {
			fmt.Printf("  DNS: %s\n", strings.TrimSpace(out))
		}
	}
}

// waitForRQLiteHealth polls RQLite until it reports Leader or Follower state.
func waitForRQLiteHealth(node inspector.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runSSHOutput(node, "curl -sf http://localhost:5001/status 2>/dev/null | grep -o '\"state\":\"[^\"]*\"'")
		if err == nil {
			result := strings.TrimSpace(out)
			if strings.Contains(result, "Leader") || strings.Contains(result, "Follower") {
				return nil
			}
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for RQLite health after %s", timeout)
}

// generateInviteToken runs `orama node invite` on the node and parses the token.
func generateInviteToken(node inspector.Node) (string, error) {
	out, err := runSSHOutput(node, "/opt/orama/bin/orama node invite --expiry 1h 2>&1")
	if err != nil {
		return "", fmt.Errorf("invite command failed: %w", err)
	}

	// Parse token from output — the invite command outputs:
	//   "sudo orama install --join https://... --token <64-char-hex> --vps-ip ..."
	// Look for the --token flag value first
	fields := strings.Fields(out)
	for i, field := range fields {
		if field == "--token" && i+1 < len(fields) {
			candidate := fields[i+1]
			if len(candidate) == 64 && isHex(candidate) {
				return candidate, nil
			}
		}
	}

	// Fallback: look for any standalone 64-char hex string
	for _, word := range fields {
		if len(word) == 64 && isHex(word) {
			return word, nil
		}
	}

	return "", fmt.Errorf("could not parse token from invite output:\n%s", out)
}

// isSafeDNSName returns true if the string is safe to use in shell commands.
func isSafeDNSName(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-') {
			return false
		}
	}
	return len(s) > 0
}

// isHex returns true if s contains only hex characters.
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// runSSHOutput runs a command via SSH and returns stdout as a string.
// Uses StrictHostKeyChecking=no because sandbox IPs are frequently recycled.
func runSSHOutput(node inspector.Node, command string) (string, error) {
	args := []string{
		"ssh", "-n",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-i", node.SSHKey,
		fmt.Sprintf("%s@%s", node.User, node.Host),
		command,
	}

	out, err := execCommand(args[0], args[1:]...)
	return string(out), err
}

// execCommand runs a command and returns its output.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// findNewestArchive finds the newest binary archive in /tmp/.
func findNewestArchive() string {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return ""
	}

	var best string
	var bestMod int64
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "orama-") && strings.Contains(name, "-linux-") && strings.HasSuffix(name, ".tar.gz") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Unix() > bestMod {
				best = filepath.Join("/tmp", name)
				bestMod = info.ModTime().Unix()
			}
		}
	}

	return best
}

// formatBytes formats a byte count as human-readable.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// printCreateSummary prints the cluster summary after creation.
func printCreateSummary(cfg *Config, state *SandboxState) {
	fmt.Printf("\nSandbox %q ready (%d nodes)\n", state.Name, len(state.Servers))
	fmt.Println()

	fmt.Println("Nameservers:")
	for _, srv := range state.NameserverNodes() {
		floating := ""
		if srv.FloatingIP != "" {
			floating = fmt.Sprintf(" (floating: %s)", srv.FloatingIP)
		}
		fmt.Printf("  %s: %s%s\n", srv.Name, srv.IP, floating)
	}

	fmt.Println("Nodes:")
	for _, srv := range state.RegularNodes() {
		fmt.Printf("  %s: %s\n", srv.Name, srv.IP)
	}

	fmt.Println()
	fmt.Printf("Domain:  %s\n", cfg.Domain)
	fmt.Printf("Gateway: https://%s\n", cfg.Domain)
	fmt.Println()
	fmt.Println("SSH:     orama sandbox ssh 1")
	fmt.Println("Destroy: orama sandbox destroy")
}

// registerNodesWithOperator tags all sandbox nodes with the operator's wallet
// via a direct RQLite UPDATE on the genesis node. This enables `orama nodes`
// to discover sandbox nodes alongside production nodes.
func registerNodesWithOperator(state *SandboxState, sshKeyPath string) {
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	addrData, err := client.GetAddress(ctx, "evm")
	if err != nil || addrData == nil || addrData.Address == "" {
		fmt.Fprintf(os.Stderr, "Warning: could not get operator wallet, nodes not tagged: %v\n", err)
		return
	}
	wallet := addrData.Address

	if len(state.Servers) == 0 {
		return
	}
	genesis := state.Servers[0]

	node := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}
	// Use RQLite's parameterized query to avoid any injection risk.
	// The JSON payload has the wallet as a parameter, not interpolated into SQL.
	payload := fmt.Sprintf(`[["UPDATE dns_nodes SET operator_wallet = ?, environment = 'sandbox' WHERE operator_wallet IS NULL OR operator_wallet = ''", %q]]`, wallet)
	cmd := fmt.Sprintf(`curl -sf -X POST http://localhost:5001/db/execute -H 'Content-Type: application/json' -d '%s'`, payload)
	if _, err := runSSHOutput(node, cmd); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to tag nodes with operator wallet: %v\n", err)
	}
}

// cleanupFailedCreate deletes any servers that were created during a failed provision.
func cleanupFailedCreate(client *HetznerClient, state *SandboxState) {
	if len(state.Servers) == 0 {
		return
	}
	fmt.Println("\nCleaning up failed creation...")
	for _, srv := range state.Servers {
		if srv.ID > 0 {
			client.DeleteServer(srv.ID)
			fmt.Printf("  Deleted %s\n", srv.Name)
		}
	}
	DeleteState(state.Name)
}
