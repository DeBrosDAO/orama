package report

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// collectOlric gathers Olric distributed cache health information.
func collectOlric() *OlricReport {
	r := &OlricReport{}

	// 1. ServiceActive: systemctl is-active orama-olric
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-olric"); err == nil {
			r.ServiceActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. MemberlistUp: check if port 3322 is listening
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
			r.MemberlistUp = portIsListening(out, 3322)
		}
	}

	// 3. RestartCount: systemctl show NRestarts
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "show", "orama-olric", "--property=NRestarts"); err == nil {
			props := parseProperties(out)
			r.RestartCount = parseInt(props["NRestarts"])
		}
	}

	// 4. ProcessMemMB: ps -C olric-server -o rss=
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ps", "-C", "olric-server", "-o", "rss=", "--no-headers"); err == nil {
			line := strings.TrimSpace(out)
			if line != "" {
				// May have multiple lines if multiple processes; take the first.
				first := strings.Fields(line)[0]
				if kb, err := strconv.Atoi(first); err == nil {
					r.ProcessMemMB = kb / 1024
				}
			}
		}
	}

	// 5. LogErrors: grep errors from journal
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(error|ERR)" || echo 0`); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.LogErrors = n
			}
		}
	}

	// 6. LogSuspects: grep suspect/marking failed/dead
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(suspect|marking.*(failed|dead))" || echo 0`); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.LogSuspects = n
			}
		}
	}

	// 7. LogFlapping: grep memberlist join/leave
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(memberlist.*(join|leave))" || echo 0`); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.LogFlapping = n
			}
		}
	}

	// 8. Member info: try HTTP GET to http://localhost:3320/
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if body, err := httpGet(ctx, "http://localhost:3320/"); err == nil {
			var info struct {
				Coordinator string `json:"coordinator"`
				Members     []struct {
					Name string `json:"name"`
				} `json:"members"`
				// Some Olric versions expose a flat member list or a different structure.
			}
			if err := json.Unmarshal(body, &info); err == nil {
				r.Coordinator = info.Coordinator
				r.MemberCount = len(info.Members)
				for _, m := range info.Members {
					r.Members = append(r.Members, m.Name)
				}
			}

			// Fallback: try to extract member count from a different JSON layout.
			if r.MemberCount == 0 {
				var raw map[string]interface{}
				if err := json.Unmarshal(body, &raw); err == nil {
					if members, ok := raw["members"]; ok {
						if arr, ok := members.([]interface{}); ok {
							r.MemberCount = len(arr)
							for _, m := range arr {
								if s, ok := m.(string); ok {
									r.Members = append(r.Members, s)
								}
							}
						}
					}
					if coord, ok := raw["coordinator"].(string); ok && r.Coordinator == "" {
						r.Coordinator = coord
					}
				}
			}
		}
	}

	return r
}

// portIsListening checks if a given port number appears in ss -tlnp output.
func portIsListening(ssOutput string, port int) bool {
	portStr := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(ssOutput, "\n") {
		if strings.Contains(line, portStr) {
			return true
		}
	}
	return false
}
