package node

import (
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose common node issues",
	Long: `Run a series of diagnostic checks on this node to identify
common issues with services, connectivity, disk space, and more.`,
	RunE: runDoctor,
}

type check struct {
	Name   string
	Status string // PASS, FAIL, WARN
	Detail string
}

func runDoctor(cmd *cobra.Command, args []string) error {
	fmt.Println("Node Doctor")
	fmt.Println("===========")
	fmt.Println()

	var checks []check

	// 1. Check if services exist
	services := utils.GetProductionServices()
	if len(services) == 0 {
		checks = append(checks, check{"Services installed", "FAIL", "No Orama services found. Run 'orama node install' first."})
	} else {
		checks = append(checks, check{"Services installed", "PASS", fmt.Sprintf("%d services found", len(services))})
	}

	// 2. Check each service status
	running := 0
	stopped := 0
	for _, svc := range services {
		active, _ := utils.IsServiceActive(svc)
		if active {
			running++
		} else {
			stopped++
		}
	}
	if stopped > 0 {
		checks = append(checks, check{"Services running", "WARN", fmt.Sprintf("%d running, %d stopped", running, stopped)})
	} else if running > 0 {
		checks = append(checks, check{"Services running", "PASS", fmt.Sprintf("All %d services running", running)})
	}

	// 3. Check RQLite health
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(constants.LocalRQLiteURL() + "/status")
	if err != nil {
		checks = append(checks, check{"RQLite reachable", "FAIL", fmt.Sprintf("Cannot connect: %v", err)})
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			checks = append(checks, check{"RQLite reachable", "PASS", fmt.Sprintf("HTTP API responding on :%d", constants.RQLiteHTTPPort)})
		} else {
			checks = append(checks, check{"RQLite reachable", "WARN", fmt.Sprintf("HTTP %d", resp.StatusCode)})
		}
	}

	// 4. Check Olric health
	resp, err = client.Get(constants.LocalOlricURL() + "/")
	if err != nil {
		checks = append(checks, check{"Olric reachable", "FAIL", fmt.Sprintf("Cannot connect: %v", err)})
	} else {
		resp.Body.Close()
		checks = append(checks, check{"Olric reachable", "PASS", fmt.Sprintf("Responding on :%d", constants.OlricHTTPPort)})
	}

	// 5. Check Gateway health
	resp, err = client.Get("http://localhost:8443/health")
	if err != nil {
		checks = append(checks, check{"Gateway reachable", "FAIL", fmt.Sprintf("Cannot connect: %v", err)})
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var health map[string]interface{}
			if json.Unmarshal(body, &health) == nil {
				if s, ok := health["status"].(string); ok {
					checks = append(checks, check{"Gateway reachable", "PASS", fmt.Sprintf("Status: %s", s)})
				} else {
					checks = append(checks, check{"Gateway reachable", "PASS", "Responding"})
				}
			} else {
				checks = append(checks, check{"Gateway reachable", "PASS", "Responding"})
			}
		} else {
			checks = append(checks, check{"Gateway reachable", "WARN", fmt.Sprintf("HTTP %d", resp.StatusCode)})
		}
	}

	// 6. Check disk space
	out, err := exec.Command("df", "-h", "/opt/orama").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 1 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				usePercent := fields[4]
				checks = append(checks, check{"Disk space (/opt/orama)", "PASS", fmt.Sprintf("Usage: %s (available: %s)", usePercent, fields[3])})
			}
		}
	}

	// 7. Check DNS resolution (basic)
	_, err = net.LookupHost("orama-devnet.network")
	if err != nil {
		checks = append(checks, check{"DNS resolution", "WARN", fmt.Sprintf("Cannot resolve orama-devnet.network: %v", err)})
	} else {
		checks = append(checks, check{"DNS resolution", "PASS", "orama-devnet.network resolves"})
	}

	// 8. Check if ports are conflicting (only for stopped services)
	ports, err := utils.CollectPortsForServices(services, true)
	if err == nil && len(ports) > 0 {
		var conflicts []string
		for _, spec := range ports {
			ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", spec.Port))
			if err != nil {
				conflicts = append(conflicts, fmt.Sprintf("%s (:%d)", spec.Name, spec.Port))
			} else {
				ln.Close()
			}
		}
		if len(conflicts) > 0 {
			checks = append(checks, check{"Port conflicts", "WARN", fmt.Sprintf("Ports in use: %s", strings.Join(conflicts, ", "))})
		} else {
			checks = append(checks, check{"Port conflicts", "PASS", "No conflicts detected"})
		}
	}

	// Print results
	maxName := 0
	for _, c := range checks {
		if len(c.Name) > maxName {
			maxName = len(c.Name)
		}
	}

	pass, fail, warn := 0, 0, 0
	for _, c := range checks {
		fmt.Printf("  [%s] %-*s  %s\n", c.Status, maxName, c.Name, c.Detail)
		switch c.Status {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		case "WARN":
			warn++
		}
	}

	fmt.Printf("\nSummary: %d passed, %d failed, %d warnings\n", pass, fail, warn)

	if fail > 0 {
		// A failing check is the answer this command exists to give, so the
		// message is the summary above, not a second error line.
		return clierr.Wrap(clierr.CodeFailure, errCheckFailed(fail))
	}
	return nil
}

// errCheckFailed names how many diagnostics failed.
func errCheckFailed(n int) error {
	return fmt.Errorf("%d diagnostic check(s) failed", n)
}
