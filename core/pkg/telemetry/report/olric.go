package report

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/olric"
)

// indexOlricUnit is the index Olric service. It replaced the pre-namespace
// orama-olric.service, which the index migration stops and disables.
const indexOlricUnit = "orama-namespace-olric@index"

// olricMembersTimeout bounds the member-list call to the index Olric.
const olricMembersTimeout = 3 * time.Second

// collectOlric gathers Olric distributed cache health information.
func collectOlric() *OlricReport {
	r := &OlricReport{}

	// 1. ServiceActive: systemctl is-active orama-namespace-olric@index
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", indexOlricUnit); err == nil {
			r.ServiceActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. MemberlistUp: check if the memberlist port is listening
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
			r.MemberlistUp = memberlistUp(out)
		}
	}

	// 3. RestartCount: systemctl show NRestarts
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "show", indexOlricUnit, "--property=NRestarts"); err == nil {
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
			`journalctl -u `+indexOlricUnit+` --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(error|ERR)" || echo 0`); err == nil {
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
			`journalctl -u `+indexOlricUnit+` --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(suspect|marking.*(failed|dead))" || echo 0`); err == nil {
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
			`journalctl -u `+indexOlricUnit+` --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(memberlist.*(join|leave))" || echo 0`); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.LogFlapping = n
			}
		}
	}

	// 8. Member info from the index Olric at the address the installer bound it to (never
	// loopback), through an Olric client call: Olric has no HTTP API on that port.
	if addr, err := config.InstalledOlricAddr(config.ProductionNodeConfigPath); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), olricMembersTimeout)
		defer cancel()
		if members, err := olric.Members(ctx, addr, olricMembersTimeout); err == nil {
			r.MemberCount = len(members)
			for _, m := range members {
				r.Members = append(r.Members, m.Name)
				if m.Coordinator {
					r.Coordinator = m.Name
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

// memberlistUp reports whether ss -tlnp output shows the index Olric
// memberlist listener (constants.OlricMemberlistPort).
func memberlistUp(ssOutput string) bool {
	return portIsListening(ssOutput, constants.OlricMemberlistPort)
}
