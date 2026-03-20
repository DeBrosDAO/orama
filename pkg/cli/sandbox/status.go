package sandbox

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// List prints all sandbox clusters.
func List() error {
	states, err := ListStates()
	if err != nil {
		return err
	}

	if len(states) == 0 {
		fmt.Println("No sandboxes found.")
		fmt.Println("Create one: orama sandbox create")
		return nil
	}

	fmt.Printf("%-20s  %-10s  %-5s  %-25s  %s\n", "NAME", "STATUS", "NODES", "CREATED", "DOMAIN")
	for _, s := range states {
		fmt.Printf("%-20s  %-10s  %-5d  %-25s  %s\n",
			s.Name, s.Status, len(s.Servers), s.CreatedAt.Format("2006-01-02 15:04"), s.Domain)
	}

	// Check for orphaned servers on Hetzner
	cfg, err := LoadConfig()
	if err != nil {
		return nil // Config not set up, skip orphan check
	}

	client := NewHetznerClient(cfg.HetznerAPIToken)
	hetznerServers, err := client.ListServersByLabel("orama-sandbox")
	if err != nil {
		return nil // API error, skip orphan check
	}

	// Build set of known server IDs
	known := make(map[int64]bool)
	for _, s := range states {
		for _, srv := range s.Servers {
			known[srv.ID] = true
		}
	}

	var orphans []string
	for _, srv := range hetznerServers {
		if !known[srv.ID] {
			orphans = append(orphans, fmt.Sprintf("%s (ID: %d, IP: %s)", srv.Name, srv.ID, srv.PublicNet.IPv4.IP))
		}
	}

	if len(orphans) > 0 {
		fmt.Printf("\nWarning: %d orphaned server(s) on Hetzner (no state file):\n", len(orphans))
		for _, o := range orphans {
			fmt.Printf("  %s\n", o)
		}
		fmt.Println("Delete manually at https://console.hetzner.cloud")
	}

	return nil
}

// Status prints the health report for a sandbox cluster.
func Status(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	state, err := resolveSandbox(name)
	if err != nil {
		return err
	}

	sshKeyPath, cleanup, err := resolveVaultKeyOnce(cfg.SSHKey.VaultTarget)
	if err != nil {
		return fmt.Errorf("prepare SSH key: %w", err)
	}
	defer cleanup()

	fmt.Printf("Sandbox: %s (status: %s)\n\n", state.Name, state.Status)

	for _, srv := range state.Servers {
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

		fmt.Printf("%s (%s) — %s\n", srv.Name, srv.IP, srv.Role)

		// Get node report
		out, err := runSSHOutput(node, "orama node report --json 2>/dev/null")
		if err != nil {
			fmt.Printf("  Status: UNREACHABLE (%v)\n", err)
			fmt.Println()
			continue
		}

		printNodeReport(out)
		fmt.Println()
	}

	// Cluster summary
	fmt.Println("Cluster Summary")
	fmt.Println("---------------")
	genesis := state.GenesisServer()
	genesisNode := inspector.Node{User: "root", Host: genesis.IP, SSHKey: sshKeyPath}

	out, err := runSSHOutput(genesisNode, "curl -sf http://localhost:5001/status 2>/dev/null")
	if err != nil {
		fmt.Println("  RQLite: UNREACHABLE")
	} else {
		var status map[string]interface{}
		if err := json.Unmarshal([]byte(out), &status); err == nil {
			if store, ok := status["store"].(map[string]interface{}); ok {
				if raft, ok := store["raft"].(map[string]interface{}); ok {
					fmt.Printf("  RQLite state: %v\n", raft["state"])
					fmt.Printf("  Commit index: %v\n", raft["commit_index"])
					if nodes, ok := raft["nodes"].([]interface{}); ok {
						fmt.Printf("  Nodes: %d\n", len(nodes))
					}
				}
			}
		}
	}

	return nil
}

// printNodeReport parses and prints a node report JSON.
func printNodeReport(jsonStr string) {
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &report); err != nil {
		fmt.Printf("  Report: (parse error)\n")
		return
	}

	// Print key fields
	if services, ok := report["services"].(map[string]interface{}); ok {
		var active, inactive []string
		for name, info := range services {
			if svc, ok := info.(map[string]interface{}); ok {
				if state, ok := svc["active"].(bool); ok && state {
					active = append(active, name)
				} else {
					inactive = append(inactive, name)
				}
			}
		}
		if len(active) > 0 {
			fmt.Printf("  Active:   %s\n", strings.Join(active, ", "))
		}
		if len(inactive) > 0 {
			fmt.Printf("  Inactive: %s\n", strings.Join(inactive, ", "))
		}
	}

	if rqlite, ok := report["rqlite"].(map[string]interface{}); ok {
		if state, ok := rqlite["state"].(string); ok {
			fmt.Printf("  RQLite:   %s\n", state)
		}
	}
}
