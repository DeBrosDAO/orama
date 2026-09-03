package report

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// collectAnyone gathers Anyone client health (and leftover relay unit state).
func collectAnyone() *AnyoneReport {
	r := &AnyoneReport{}

	// 1. RelayActive: systemctl is-active orama-anyone-relay
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-anyone-relay"); err == nil {
			r.RelayActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. ClientActive: systemctl is-active orama-anyone-client
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-namespace-anyone-client@index"); err == nil && strings.TrimSpace(out) == "active" {
			r.ClientActive = true
		} else if out, err := runCmd(ctx, "systemctl", "is-active", "orama-anyone-client"); err == nil {
			r.ClientActive = strings.TrimSpace(out) == "active"
		}
	}

	// 3. Mode: client if the client unit is up, even when a leftover relay unit exists.
	if r.ClientActive {
		r.Mode = "client"
	} else if r.RelayActive {
		r.Mode = "relay"
	}

	// 4. ORPortListening, SocksListening, ControlListening: check ports in ss -tlnp
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
			r.ORPortListening = portIsListening(out, 9001)
			r.SocksListening = portIsListening(out, 9050)
			r.ControlListening = portIsListening(out, 9051)
		}
	}

	// 5. Bootstrapped / BootstrapPct: parse last "Bootstrapped" line from notices.log
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`grep "Bootstrapped" /var/log/anon/notices.log 2>/dev/null | tail -1`); err == nil {
			out = strings.TrimSpace(out)
			if out != "" {
				// Parse percentage from lines like:
				// "... Bootstrapped 100% (done): Done"
				// "... Bootstrapped 85%: Loading relay descriptors"
				re := regexp.MustCompile(`Bootstrapped\s+(\d+)%`)
				if m := re.FindStringSubmatch(out); len(m) >= 2 {
					if pct, err := strconv.Atoi(m[1]); err == nil {
						r.BootstrapPct = pct
						r.Bootstrapped = pct == 100
					}
				}
			}
		}
	}

	// 6. Fingerprint: read /var/lib/anon/fingerprint
	if data, err := os.ReadFile("/var/lib/anon/fingerprint"); err == nil {
		line := strings.TrimSpace(string(data))
		// The file may contain "nickname fingerprint" — extract just the fingerprint.
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			r.Fingerprint = fields[1]
		} else if len(fields) == 1 {
			r.Fingerprint = fields[0]
		}
	}

	// 7. Nickname: extract from anonrc config
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`grep "^Nickname" /etc/anon/anonrc 2>/dev/null | awk '{print $2}'`); err == nil {
			r.Nickname = strings.TrimSpace(out)
		}
	}

	return r
}
