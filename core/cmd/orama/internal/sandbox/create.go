package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// Create orchestrates the creation of a new sandbox cluster.
func Create(name, archive string) error {
	if name == "" {
		name = GenerateName()
	}
	if err := validateName(name); err != nil {
		return err
	}

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

	// 3. The operator's wallet: the genesis node's archive trust anchor, and
	// so the only signer this sandbox installs builds from.
	wallet, err := readOperatorWallet()
	if err != nil {
		return err
	}
	fmt.Printf("  [ok] Operator wallet: %s\n", wallet)

	// 4. Resolve SSH key (may trigger approval prompt in RootWallet app)
	fmt.Print("  [..] Resolving SSH key from vault...")
	sshKeyPath, cleanup, err := resolveVaultKeyOnce(cfg.SSHKey.VaultTarget)
	if err != nil {
		fmt.Println(" FAILED")
		return fmt.Errorf("prepare SSH key: %w", err)
	}
	defer cleanup()
	fmt.Println(" ok")

	// 5. The build to deploy: the one named, or one built now from this
	// checkout — never "the newest archive in /tmp", which may be another's.
	// It must verify against the wallet before any server is paid for; the
	// upload verifies it again before anything reaches a server.
	archivePath, err := resolveArchive(archive)
	if err != nil {
		return err
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat archive %s: %w", archivePath, err)
	}
	if _, err := archivetrust.VerifyArchiveFile(archivePath, []string{wallet}); err != nil {
		return fmt.Errorf("the archive does not verify against your wallet %s: %w", wallet, err)
	}
	fmt.Printf("  [ok] Binary archive: %s (%s), signed by your wallet\n", filepath.Base(archivePath), printer.FormatBytes(info.Size()))

	// 6. Verify Hetzner API token works
	client := NewHetznerClient(cfg.HetznerAPIToken)
	if err := client.ValidateToken(); err != nil {
		return fmt.Errorf("hetzner API: %w\n     Check your token in ~/.orama/sandbox.yaml", err)
	}
	fmt.Println("  [ok] Hetzner API token valid")

	fmt.Println()

	// --- All preflight checks passed, proceed ---

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

	// Pin every server's host key before anything is sent to it.
	fmt.Println("\nPinning SSH host keys...")
	if err := pinHostKeys(state, sshReadyTimeout); err != nil {
		state.Status = StatusError
		_ = SaveState(state)
		return fmt.Errorf("pin host keys: %w", err)
	}
	nodes, err := pinnedNodes(state, sshKeyPath)
	if err != nil {
		return err
	}

	// Phase 2: Assign floating IPs
	fmt.Println("\nPhase 2: Assigning floating IPs...")
	if err := phase2AssignFloatingIPs(client, cfg, state, nodes); err != nil {
		return fmt.Errorf("assign floating IPs: %w", err)
	}
	if err := SaveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: save state after floating IPs: %v\n", err)
	}

	// Phase 3: Put the verified archive on every server
	fmt.Println("\nPhase 3: Uploading the verified archive...")
	if err := phase3UploadArchive(nodes, archivePath, wallet); err != nil {
		state.Status = StatusError
		_ = SaveState(state)
		return fmt.Errorf("upload archive: %w", err)
	}

	// Phase 4: Install genesis node
	fmt.Println("\nPhase 4: Installing genesis node...")
	if err := phase4InstallGenesis(cfg, state, nodes, wallet); err != nil {
		state.Status = StatusError
		_ = SaveState(state)
		return fmt.Errorf("install genesis: %w", err)
	}

	// Phase 5: Join remaining nodes
	fmt.Println("\nPhase 5: Joining remaining nodes...")
	if err := phase5JoinNodes(cfg, state, nodes, wallet); err != nil {
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

	if err := registerEnvironment(cfg, state); err != nil {
		return fmt.Errorf("sandbox %q is running, but: %w", state.Name, err)
	}

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
			return fmt.Errorf("rootwallet agent is not reachable\n\n  Open the RootWallet desktop app and unlock it.")
		}
		return fmt.Errorf("rootwallet agent: %w", err)
	}

	return validateAgentStatus(status)
}

// validateAgentStatus checks that the agent status indicates readiness.
// Separated from checkAgentReady for testability.
func validateAgentStatus(status *rwagent.StatusResponse) error {
	if status.Locked {
		// A prompt already on screen is the difference between "unlock it" and
		// "you have one waiting" — the agent reports the count and this client
		// used to drop it.
		if status.PendingUnlocks > 0 {
			return fmt.Errorf("rootwallet agent is locked\n\n  %d approval prompt(s) are already waiting in the RootWallet desktop app — answer them.", status.PendingUnlocks)
		}
		return fmt.Errorf("rootwallet agent is locked\n\n  Unlock it in the RootWallet desktop app.")
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
// nodes are the servers as pinned SSH targets, in state.Servers order.
func phase2AssignFloatingIPs(client *HetznerClient, cfg *Config, state *SandboxState, nodes []inspector.Node) error {
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
		cmd := fmt.Sprintf("ip addr add %s/32 dev lo 2>/dev/null || true", fip.IP)
		if err := remotessh.RunSSHStreaming(nodes[i], cmd); err != nil {
			return fmt.Errorf("configure loopback on %s: %w", srv.Name, err)
		}
	}

	return nil
}

// resolveArchive returns archive when it names one, and otherwise builds this
// checkout and returns exactly the file the build wrote.
func resolveArchive(archive string) (string, error) {
	if archive != "" {
		return archive, nil
	}
	fmt.Println("  [--] No --archive given, building this checkout...")
	builder := build.NewBuilder(&build.Flags{Arch: "amd64"})
	if err := builder.Build(); err != nil {
		return "", fmt.Errorf("build archive: %w", err)
	}
	return builder.OutputPath(), nil
}

// phase3UploadArchive puts the archive on every pinned node through node
// setup's verified path: verified here against the operator's wallet,
// uploaded as the canonical re-pack, and staged on the server by
// `node stage-archive`, which creates its trust anchor from the wallet.
func phase3UploadArchive(nodes []inspector.Node, archivePath, wallet string) error {
	fmt.Printf("  Archive: %s\n", filepath.Base(archivePath))
	if err := installArchive(nodes, archivePath, wallet); err != nil {
		return err
	}
	fmt.Println("  All nodes ready")
	return nil
}

// phase4InstallGenesis installs the genesis node, whose archive trust anchor
// is the operator's wallet.
func phase4InstallGenesis(cfg *Config, state *SandboxState, nodes []inspector.Node, wallet string) error {
	genesis := state.GenesisServer()
	node := nodes[0]

	fmt.Printf("  Installing on %s (%s)...\n", genesis.Name, genesis.IP)
	if err := remotessh.RunSSHStreaming(node, genesisInstallCommand(cfg, genesis, wallet)); err != nil {
		return fmt.Errorf("install genesis: %w", err)
	}

	// Wait for RQLite leader
	fmt.Print("  Waiting for RQLite leader...")
	if err := waitForRQLiteHealth(node, 3*time.Minute); err != nil {
		return fmt.Errorf("genesis health: %w", err)
	}
	fmt.Println(" OK")

	// An invite pins the certificate genesis serves, so there must be one.
	fmt.Print("  Waiting for the genesis TLS certificate...")
	if err := waitForCertificate(net.JoinHostPort(genesis.IP, httpsPort), cfg.Domain, certificateTimeout); err != nil {
		return fmt.Errorf("genesis certificate: %w", err)
	}
	fmt.Println(" OK")

	return nil
}

// phase5JoinNodes joins the remaining 4 nodes to the cluster (serial).
// Generates invite tokens just-in-time to avoid expiry during long installs.
func phase5JoinNodes(cfg *Config, state *SandboxState, nodes []inspector.Node, wallet string) error {
	for i := 1; i < len(state.Servers); i++ {
		srv := state.Servers[i]
		node := nodes[i]

		// Mint the invite just before use to avoid expiry
		invite, err := mintInvite(nodes[0])
		if err != nil {
			return fmt.Errorf("invite for %s: %w", srv.Name, err)
		}

		fmt.Printf("  [%d/%d] Joining %s (%s, %s)...\n", i, len(state.Servers)-1, srv.Name, srv.IP, srv.Role)
		joinCmd, secrets, err := joinInstallCommand(cfg, srv, wallet, invite)
		if err != nil {
			return fmt.Errorf("join %s: %w", srv.Name, err)
		}
		if err := remotessh.RunSSHStreaming(node, joinCmd, remotessh.WithStdin(bytes.NewReader(secrets))); err != nil {
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
	out, err := runSSHOutput(node, rqlite.NodeShellCurl(remotessh.SudoPrefix(node), "-s", "/status")+` | grep -o '"state":"[^"]*"' | head -1`)
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
// node is a pinned sandbox node (pinnedNodes).
func waitForRQLiteHealth(node inspector.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := remotessh.RunSSHOutput(node, rqlite.NodeShellCurl(remotessh.SudoPrefix(node), "-sf", "/status")+` 2>/dev/null | grep -o '"state":"[^"]*"'`)
		if err == nil {
			result := strings.TrimSpace(out)
			if strings.Contains(result, "Leader") || strings.Contains(result, "Follower") {
				return nil
			}
		}
		time.Sleep(readinessPollInterval)
	}
	return fmt.Errorf("timeout waiting for RQLite health after %s", timeout)
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
