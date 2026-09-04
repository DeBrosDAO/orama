package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"

	"context"

	"net/http"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

const (
	maintenanceFlagPath = "/opt/orama/.orama/maintenance.flag"

	// indexNamespace is the reserved namespace whose rqlite is the cluster
	// registry. It lives under data/namespaces like a tenant but is not one.
	indexNamespace = "index"
)

// HandlePreUpgrade prepares the node for a safe rolling upgrade:
//  1. Checks quorum safety
//  2. Writes maintenance flag
//  3. Transfers leadership on the index RQLite if leader
//  4. Transfers leadership on each namespace RQLite
//  5. Waits 15s for metadata propagation (H5 fix)
func HandlePreUpgrade() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: pre-upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Pre-upgrade: preparing node for safe restart...\n")

	// 1. Check quorum safety
	if warning := checkQuorumSafety(); warning != "" {
		fmt.Fprintf(os.Stderr, "  UNSAFE: %s\n", warning)
		fmt.Fprintf(os.Stderr, "  Aborting pre-upgrade. Use 'orama node stop --force' to override.\n")
		os.Exit(1)
	}
	fmt.Printf("  Quorum check passed\n")

	// 2. Write maintenance flag
	if err := os.MkdirAll(filepath.Dir(maintenanceFlagPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: failed to create flag directory: %v\n", err)
	}
	if err := os.WriteFile(maintenanceFlagPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: failed to write maintenance flag: %v\n", err)
	} else {
		fmt.Printf("  Maintenance flag written\n")
	}

	// 3. Transfer leadership on the index RQLite
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	fmt.Printf("  Checking index RQLite leadership (port %d)...\n", constants.RQLiteHTTPPort)
	if err := rqlite.TransferLeadership(constants.RQLiteHTTPPort, logger); err != nil {
		fmt.Fprintf(os.Stderr, "  UNSAFE: this node still leads the index RQLite: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Restarting it now forces an election and fails in-flight writes.\n")
		fmt.Fprintf(os.Stderr, "  Aborting pre-upgrade. Check the other voters are reachable, then retry.\n")
		os.Exit(1)
	}
	fmt.Printf("  Index RQLite leadership handled\n")

	// 4. Transfer leadership on each namespace RQLite.
	//
	// getNamespaceRQLitePorts walks data/namespaces, which contains an "index"
	// directory too, so the index would otherwise be transferred a second time -
	// harmless but confusing in the log, and a second chance to abort on a node
	// that already stepped down.
	nsPorts := getNamespaceRQLitePorts()
	delete(nsPorts, indexNamespace)
	for ns, port := range nsPorts {
		fmt.Printf("  Checking namespace '%s' RQLite leadership (port %d)...\n", ns, port)
		if err := rqlite.TransferLeadership(port, logger); err != nil {
			// A tenant namespace losing its leader degrades that namespace, not
			// the node's ability to restart safely, so this stays a warning.
			fmt.Printf("  Warning: namespace '%s' leadership transfer: %v\n", ns, err)
		} else {
			fmt.Printf("  Namespace '%s' RQLite leadership handled\n", ns)
		}
	}

	// 5. Confirm another node is actually leading before we stop.
	//
	// This was a flat 15-second sleep "for metadata propagation". A sleep
	// cannot tell a cluster that elected a new leader in two seconds from one
	// that never did, and stopping this node in the second case removes a voter
	// from a cluster with no leader.
	//
	// TransferLeadership already waits for this node to stop being the leader.
	// What is left to confirm is that somebody else started: a cluster where
	// this node stepped down and nobody was elected has no quorum, and is the
	// one state in which restarting is worse than doing nothing.
	fmt.Printf("  Confirming another node has taken leadership...\n")
	if err := waitForOtherLeader(constants.RQLiteHTTPPort, leaderHandoverBudget); err != nil {
		fmt.Fprintf(os.Stderr, "  UNSAFE: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Stopping this node now would remove a voter from a cluster with no leader.\n")
		fmt.Fprintf(os.Stderr, "  Aborting pre-upgrade. Check the other voters are reachable, then retry.\n")
		os.Exit(1)
	}
	fmt.Printf("  Another node is leading; safe to restart\n")

	fmt.Printf("Pre-upgrade complete. Node is ready for restart.\n")
}

// getNamespaceRQLitePorts scans namespace env files to find RQLite HTTP ports.
// Returns map of namespace_name → HTTP port.
func getNamespaceRQLitePorts() map[string]int {
	namespacesDir := "/opt/orama/.orama/data/namespaces"
	ports := make(map[string]int)

	entries, err := os.ReadDir(namespacesDir)
	if err != nil {
		return ports
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		envFile := filepath.Join(namespacesDir, ns, "rqlite.env")
		port := parseHTTPPortFromEnv(envFile)
		if port > 0 {
			ports[ns] = port
		}
	}

	return ports
}

// parseHTTPPortFromEnv reads an env file and extracts the HTTP port from
// the HTTP_ADDR=0.0.0.0:PORT line.
func parseHTTPPortFromEnv(envFile string) int {
	f, err := os.Open(envFile)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "HTTP_ADDR=") {
			addr := strings.TrimPrefix(line, "HTTP_ADDR=")
			// Format: 0.0.0.0:PORT
			if idx := strings.LastIndex(addr, ":"); idx >= 0 {
				if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
					return port
				}
			}
		}
	}
	return 0
}

// leaderHandoverBudget is how long another voter has to win the election this
// node's step-down triggered.
const leaderHandoverBudget = 60 * time.Second

// waitForOtherLeader blocks until this node reports a leader that is not
// itself.
//
// A node that is a Follower with an empty leader_id is in a cluster that cannot
// commit a write; "Follower" alone is not the safety property, "somebody is
// leading" is.
func waitForOtherLeader(port int, budget time.Duration) error {
	target := nodehealth.Target{RQLiteBase: fmt.Sprintf("http://localhost:%d", port)}
	client := &http.Client{Timeout: 5 * time.Second}

	deadline := time.Now().Add(budget)
	last := "unknown"
	for {
		st, err := nodehealth.Observe(context.Background(), client, target)
		if err == nil {
			last = fmt.Sprintf("state %s, leader %q", st.RaftState, st.LeaderID)
			if !strings.EqualFold(st.RaftState, "Leader") && st.LeaderID != "" {
				return nil
			}
		} else {
			last = err.Error()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("no other node took leadership within %s (%s)", budget, last)
		}
		time.Sleep(2 * time.Second)
	}
}
