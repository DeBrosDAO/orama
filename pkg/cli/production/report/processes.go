package report

import (
	"context"
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

			// Orphans: PPID == 1 and command contains an orama-related name.
			if ppid == 1 && isOramaProcess(command) {
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
