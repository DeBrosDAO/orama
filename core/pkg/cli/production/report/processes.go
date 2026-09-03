package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// oramaProcessNames lists command substrings that identify orama-related processes.
var oramaProcessNames = []string{
	"orama", "rqlite", "olric", "ipfs", "caddy", "coredns",
}

// collectProcesses gathers zombie/orphan process info and panic counts from logs.
func collectProcesses() *ProcessReport {
	r := &ProcessReport{}

	// Collect known systemd-managed PIDs to avoid false positive orphan detection.
	// Processes with PPID=1 that are systemd-managed daemons are NOT orphans.
	managedPIDs := collectManagedPIDs()

	// Run ps once and reuse the output for both zombies and orphans.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	out, err := runCmd(ctx, "ps", "-eo", "pid,ppid,state,comm", "--no-headers")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}

			pid, _ := strconv.Atoi(fields[0])
			ppid, _ := strconv.Atoi(fields[1])
			state := fields[2]
			command := strings.Join(fields[3:], " ")

			proc := ProcessInfo{
				PID:     pid,
				PPID:    ppid,
				State:   state,
				Command: command,
			}

			// Zombies: state == "Z"
			if state == "Z" {
				r.Zombies = append(r.Zombies, proc)
			}

			// Orphans: PPID == 1 and command is orama-related,
			// but NOT a known systemd-managed service PID.
			if ppid == 1 && isOramaProcess(command) && !managedPIDs[pid] {
				r.Orphans = append(r.Orphans, proc)
			}
		}
	}

	r.ZombieCount = len(r.Zombies)
	r.OrphanCount = len(r.Orphans)

	// PanicCount: check journal for panic/fatal in last hour.
	{
		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()

		out, err := runCmd(ctx2, "bash", "-c",
			`journalctl -u orama-node --no-pager -n 500 --since "1 hour ago" 2>/dev/null | grep -ciE "(panic|fatal)" || echo 0`)
		if err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.PanicCount = n
			}
		}
	}

	return r
}

// managedServiceUnits lists systemd units whose MainPID should be excluded from orphan detection.
var managedServiceUnits = []string{
	"orama-node",
	"orama-namespace-olric@index",
	"orama-namespace-ipfs@index",
	"orama-namespace-ipfs-cluster@index",
	"orama-namespace-vault@index",
	"orama-namespace-anyone-client@index",
	"orama-namespace-caddy@index",
	"orama-namespace-wireguard@index",
	"orama-namespace-rqlite@index",
	"orama-namespace-gateway@index",
	"orama-namespace-pubsub@index",
	"orama-namespace-coredns@nameserver",
	"rqlited",
}

// collectManagedPIDs queries systemd for the MainPID of each known service.
// Returns a set of PIDs that are legitimately managed by systemd (not orphans).
func collectManagedPIDs() map[int]bool {
	// Hard deadline: stop querying if this takes too long (e.g., node with many namespaces).
	deadline := time.Now().Add(10 * time.Second)
	pids := make(map[int]bool)

	// Collect PIDs from global services.
	for _, unit := range managedServiceUnits {
		addMainPID(pids, unit)
	}

	// Collect PIDs from namespace service instances.
	// Scan the namespaces data directory (same pattern as GetProductionServices).
	namespacesDir := "/opt/orama/.orama/data/namespaces"
	nsEntries, err := os.ReadDir(namespacesDir)
	if err == nil {
		nsServiceTypes := []string{
			"rqlite", "olric", "gateway", "sfu", "turn", "pubsub",
			"wireguard", "ipfs", "ipfs-cluster", "vault", "caddy",
			"ntfy", "anyone-client", "sni-router", "coredns",
		}
		for _, nsEntry := range nsEntries {
			if !nsEntry.IsDir() {
				continue
			}
			if time.Now().After(deadline) {
				break
			}
			ns := nsEntry.Name()
			for _, svcType := range nsServiceTypes {
				envFile := filepath.Join(namespacesDir, ns, svcType+".env")
				if _, err := os.Stat(envFile); err == nil {
					unit := fmt.Sprintf("orama-namespace-%s@%s", svcType, ns)
					addMainPID(pids, unit)
				}
			}
		}
	}

	return pids
}

// addMainPID queries systemd for a unit's MainPID and adds it to the set.
func addMainPID(pids map[int]bool, unit string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	out, err := runCmd(ctx, "systemctl", "show", unit, "--property=MainPID")
	cancel()
	if err != nil {
		return
	}
	props := parseProperties(out)
	if pidStr, ok := props["MainPID"]; ok {
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			pids[pid] = true
		}
	}
}

// isOramaProcess checks if a command string contains any orama-related process name.
func isOramaProcess(command string) bool {
	lower := strings.ToLower(command)
	for _, name := range oramaProcessNames {
		if strings.Contains(lower, name) {
			return true
		}
	}
	return false
}
