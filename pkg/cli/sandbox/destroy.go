package sandbox

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Destroy tears down a sandbox cluster.
func Destroy(name string, force bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	// Resolve sandbox name
	state, err := resolveSandbox(name)
	if err != nil {
		return err
	}

	// Confirm destruction
	if !force {
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Destroy sandbox %q? This deletes %d servers. [y/N]: ", state.Name, len(state.Servers))
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(strings.ToLower(choice))
		if choice != "y" && choice != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	state.Status = StatusDestroying
	SaveState(state) // best-effort status update

	client := NewHetznerClient(cfg.HetznerAPIToken)

	// Step 1: Unassign floating IPs from nameserver nodes
	fmt.Println("Unassigning floating IPs...")
	for _, srv := range state.NameserverNodes() {
		if srv.FloatingIP == "" {
			continue
		}
		// Find the floating IP ID from config
		for _, fip := range cfg.FloatingIPs {
			if fip.IP == srv.FloatingIP {
				if err := client.UnassignFloatingIP(fip.ID); err != nil {
					fmt.Fprintf(os.Stderr, "  Warning: could not unassign floating IP %s: %v\n", fip.IP, err)
				} else {
					fmt.Printf("  Unassigned %s from %s\n", fip.IP, srv.Name)
				}
				break
			}
		}
	}

	// Step 2: Delete all servers in parallel
	fmt.Printf("Deleting %d servers...\n", len(state.Servers))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string

	for _, srv := range state.Servers {
		wg.Add(1)
		go func(srv ServerState) {
			defer wg.Done()
			if err := client.DeleteServer(srv.ID); err != nil {
				// Treat 404 as already deleted (idempotent)
				if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
					fmt.Printf("  %s (ID %d): already deleted\n", srv.Name, srv.ID)
				} else {
					mu.Lock()
					failed = append(failed, fmt.Sprintf("%s (ID %d): %v", srv.Name, srv.ID, err))
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "  Warning: failed to delete %s: %v\n", srv.Name, err)
				}
			} else {
				fmt.Printf("  Deleted %s (ID %d)\n", srv.Name, srv.ID)
			}
		}(srv)
	}
	wg.Wait()

	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "\nFailed to delete %d server(s):\n", len(failed))
		for _, f := range failed {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "\nManual cleanup: delete servers at https://console.hetzner.cloud\n")
		state.Status = StatusError
		SaveState(state)
		return fmt.Errorf("failed to delete %d server(s)", len(failed))
	}

	// Step 3: Remove state file
	if err := DeleteState(state.Name); err != nil {
		return fmt.Errorf("delete state: %w", err)
	}

	fmt.Printf("\nSandbox %q destroyed (%d servers deleted)\n", state.Name, len(state.Servers))
	return nil
}

// resolveSandbox finds a sandbox by name or returns the active one.
func resolveSandbox(name string) (*SandboxState, error) {
	if name != "" {
		return LoadState(name)
	}

	// Find the active sandbox
	active, err := FindActiveSandbox()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, fmt.Errorf("no active sandbox found, specify --name")
	}
	return active, nil
}
