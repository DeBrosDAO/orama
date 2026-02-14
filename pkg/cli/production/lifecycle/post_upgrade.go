package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandlePostUpgrade brings the node back online after an upgrade:
//  1. Resets failed + unmasks + enables all services
//  2. Starts services in dependency order
//  3. Waits for global RQLite to be ready
//  4. Waits for each namespace RQLite to be ready
//  5. Removes maintenance flag
func HandlePostUpgrade() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: post-upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Post-upgrade: bringing node back online...\n")

	// 1. Get all services
	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  Warning: no Orama services found\n")
		return
	}

	// Reset failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Unmask and enable all services
	for _, svc := range services {
		masked, err := utils.IsServiceMasked(svc)
		if err == nil && masked {
			exec.Command("systemctl", "unmask", svc).Run()
		}
		enabled, err := utils.IsServiceEnabled(svc)
		if err == nil && !enabled {
			exec.Command("systemctl", "enable", svc).Run()
		}
	}
	fmt.Printf("  Services reset and enabled\n")

	// 2. Start services in dependency order
	fmt.Printf("  Starting services...\n")
	utils.StartServicesOrdered(services, "start")
	fmt.Printf("  Services started\n")

	// 3. Wait for global RQLite (port 5001) to be ready
	fmt.Printf("  Waiting for global RQLite (port 5001)...\n")
	if err := waitForRQLiteReady(5001, 120*time.Second); err != nil {
		fmt.Printf("  Warning: global RQLite not ready: %v\n", err)
	} else {
		fmt.Printf("  Global RQLite ready\n")
	}

	// 4. Wait for each namespace RQLite with a global timeout of 5 minutes
	nsPorts := getNamespaceRQLitePorts()
	if len(nsPorts) > 0 {
		fmt.Printf("  Waiting for %d namespace RQLite instances...\n", len(nsPorts))
		globalDeadline := time.Now().Add(5 * time.Minute)

		healthy := 0
		failed := 0
		for ns, port := range nsPorts {
			remaining := time.Until(globalDeadline)
			if remaining <= 0 {
				fmt.Printf("    Warning: global timeout reached, skipping remaining namespaces\n")
				failed += len(nsPorts) - healthy - failed
				break
			}
			timeout := 90 * time.Second
			if remaining < timeout {
				timeout = remaining
			}
			fmt.Printf("    Waiting for namespace '%s' (port %d)...\n", ns, port)
			if err := waitForRQLiteReady(port, timeout); err != nil {
				fmt.Printf("    Warning: namespace '%s' RQLite not ready: %v\n", ns, err)
				failed++
			} else {
				fmt.Printf("    Namespace '%s' ready\n", ns)
				healthy++
			}
		}
		fmt.Printf("  Namespace RQLite: %d healthy, %d failed\n", healthy, failed)
	}

	// 5. Remove maintenance flag
	if err := os.Remove(maintenanceFlagPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  Warning: failed to remove maintenance flag: %v\n", err)
	} else {
		fmt.Printf("  Maintenance flag removed\n")
	}

	fmt.Printf("Post-upgrade complete. Node is back online.\n")
}

// waitForRQLiteReady polls an RQLite instance's /status endpoint until it
// reports Leader or Follower state, or the timeout expires.
func waitForRQLiteReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/status", port)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var status struct {
			Store struct {
				Raft struct {
					State string `json:"state"`
				} `json:"raft"`
			} `json:"store"`
		}
		if err := json.Unmarshal(body, &status); err == nil {
			state := status.Store.Raft.State
			if state == "Leader" || state == "Follower" {
				return nil
			}
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout after %s waiting for RQLite on port %d", timeout, port)
}
