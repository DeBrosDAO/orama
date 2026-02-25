package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Create orchestrates the creation of a new sandbox cluster.
func Create(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	// Check for existing active sandbox
	active, err := FindActiveSandbox()
	if err != nil {
		return err
	}
	if active != nil {
		return fmt.Errorf("sandbox %q is already active (status: %s)\nDestroy it first: orama sandbox destroy --name %s",
			active.Name, active.Status, active.Name)
	}

	// Generate name if not provided
	if name == "" {
		name = GenerateName()
	}

	fmt.Printf("Creating sandbox %q (%s, %d nodes)\n\n", name, cfg.Domain, 5)

	client := NewHetznerClient(cfg.HetznerAPIToken)

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
	SaveState(state)

	// Phase 2: Assign floating IPs
	fmt.Println("\nPhase 2: Assigning floating IPs...")
	if err := phase2AssignFloatingIPs(client, cfg, state); err != nil {
		return fmt.Errorf("assign floating IPs: %w", err)
	}
	SaveState(state)

	// Phase 3: Upload binary archive
	fmt.Println("\nPhase 3: Uploading binary archive...")
	if err := phase3UploadArchive(cfg, state); err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}

	// Phase 4: Install genesis node
	fmt.Println("\nPhase 4: Installing genesis node...")
	tokens, err := phase4InstallGenesis(cfg, state)
	if err != nil {
		state.Status = StatusError
		SaveState(state)
		return fmt.Errorf("install genesis: %w", err)
	}

	// Phase 5: Join remaining nodes
	fmt.Println("\nPhase 5: Joining remaining nodes...")
	if err := phase5JoinNodes(cfg, state, tokens); err != nil {
		state.Status = StatusError
		SaveState(state)
		return fmt.Errorf("join nodes: %w", err)
	}

	// Phase 6: Verify cluster
	fmt.Println("\nPhase 6: Verifying cluster...")
	phase6Verify(cfg, state)

	state.Status = StatusRunning
	SaveState(state)

	printCreateSummary(cfg, state)
	return nil
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
	for i := 0; i < 5; i++ {
		r := <-results
		if r.err != nil {
			return fmt.Errorf("server %d: %w", r.index+1, r.err)
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
func phase2AssignFloatingIPs(client *HetznerClient, cfg *Config, state *SandboxState) error {
	sshKeyPath := cfg.ExpandedPrivateKeyPath()

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
		if err := waitForSSH(node, 2*time.Minute); err != nil {
			return fmt.Errorf("SSH not ready on %s: %w", srv.Name, err)
		}

		cmd := fmt.Sprintf("ip addr add %s/32 dev lo 2>/dev/null || true", fip.IP)
		if err := remotessh.RunSSHStreaming(node, cmd); err != nil {
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

// phase3UploadArchive builds (if needed) and uploads the binary archive to all nodes.
func phase3UploadArchive(cfg *Config, state *SandboxState) error {
	// Find existing archive
	archivePath := findNewestArchive()
	if archivePath == "" {
		fmt.Println("  No binary archive found, run `orama build` first")
		return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
	}

	info, _ := os.Stat(archivePath)
	fmt.Printf("  Archive: %s (%s)\n", filepath.Base(archivePath), formatBytes(info.Size()))

	sshKeyPath := cfg.ExpandedPrivateKeyPath()
	remotePath := "/tmp/" + filepath.Base(archivePath)

	// Upload to all 5 nodes in parallel
	var wg sync.WaitGroup
	errs := make([]error, len(state.Servers))

	for i, srv := range state.Servers {
		wg.Add(1)
		go func(idx int, srv ServerState) {
			defer wg.Done()
			node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

			if err := remotessh.UploadFile(node, archivePath, remotePath); err != nil {
				errs[idx] = fmt.Errorf("upload to %s: %w", srv.Name, err)
				return
			}

			// Extract + install CLI
			extractCmd := fmt.Sprintf("mkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s && cp /opt/orama/bin/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama",
				remotePath, remotePath)
			if err := remotessh.RunSSHStreaming(node, extractCmd); err != nil {
				errs[idx] = fmt.Errorf("extract on %s: %w", srv.Name, err)
				return
			}
			fmt.Printf("  Uploaded to %s\n", srv.Name)
		}(i, srv)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}

// phase4InstallGenesis installs the genesis node and generates invite tokens.
func phase4InstallGenesis(cfg *Config, state *SandboxState) ([]string, error) {
	genesis := state.GenesisServer()
	sshKeyPath := cfg.ExpandedPrivateKeyPath()
	node := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}

	// Install genesis
	installCmd := fmt.Sprintf("orama node install --vps-ip %s --domain %s --base-domain %s --nameserver --skip-checks",
		genesis.IP, cfg.Domain, cfg.Domain)
	fmt.Printf("  Installing on %s (%s)...\n", genesis.Name, genesis.IP)
	if err := remotessh.RunSSHStreaming(node, installCmd); err != nil {
		return nil, fmt.Errorf("install genesis: %w", err)
	}

	// Wait for RQLite leader
	fmt.Print("  Waiting for RQLite leader...")
	if err := waitForRQLiteHealth(node, 3*time.Minute); err != nil {
		return nil, fmt.Errorf("genesis health: %w", err)
	}
	fmt.Println(" OK")

	// Generate invite tokens (one per remaining node)
	fmt.Print("  Generating invite tokens...")
	remaining := len(state.Servers) - 1
	tokens := make([]string, remaining)

	for i := 0; i < remaining; i++ {
		token, err := generateInviteToken(node)
		if err != nil {
			return nil, fmt.Errorf("generate invite token %d: %w", i+1, err)
		}
		tokens[i] = token
		fmt.Print(".")
	}
	fmt.Println(" OK")

	return tokens, nil
}

// phase5JoinNodes joins the remaining 4 nodes to the cluster (serial).
func phase5JoinNodes(cfg *Config, state *SandboxState, tokens []string) error {
	genesisIP := state.GenesisServer().IP
	sshKeyPath := cfg.ExpandedPrivateKeyPath()

	for i := 1; i < len(state.Servers); i++ {
		srv := state.Servers[i]
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}
		token := tokens[i-1]

		var installCmd string
		if srv.Role == "nameserver" {
			installCmd = fmt.Sprintf("orama node install --join http://%s --token %s --vps-ip %s --domain %s --base-domain %s --nameserver --skip-checks",
				genesisIP, token, srv.IP, cfg.Domain, cfg.Domain)
		} else {
			installCmd = fmt.Sprintf("orama node install --join http://%s --token %s --vps-ip %s --base-domain %s --skip-checks",
				genesisIP, token, srv.IP, cfg.Domain)
		}

		fmt.Printf("  [%d/%d] Joining %s (%s, %s)...\n", i, len(state.Servers)-1, srv.Name, srv.IP, srv.Role)
		if err := remotessh.RunSSHStreaming(node, installCmd); err != nil {
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
func phase6Verify(cfg *Config, state *SandboxState) {
	sshKeyPath := cfg.ExpandedPrivateKeyPath()
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
	out, err := runSSHOutput(node, "orama node invite --expiry 1h 2>&1")
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
func runSSHOutput(node inspector.Node, command string) (string, error) {
	args := []string{
		"ssh", "-n",
		"-o", "StrictHostKeyChecking=accept-new",
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
