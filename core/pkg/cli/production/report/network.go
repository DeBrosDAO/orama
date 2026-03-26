package report

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// collectNetwork gathers network connectivity, TCP stats, listening ports,
// and firewall status.
func collectNetwork() *NetworkReport {
	r := &NetworkReport{}

	// 1. InternetReachable: ping 8.8.8.8
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if _, err := runCmd(ctx, "ping", "-c", "1", "-W", "2", "8.8.8.8"); err == nil {
			r.InternetReachable = true
		}
	}

	// 2. DefaultRoute: ip route show default
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ip", "route", "show", "default"); err == nil {
			r.DefaultRoute = strings.TrimSpace(out) != ""
		}
	}

	// 3. WGRouteExists: ip route show dev wg0
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ip", "route", "show", "dev", "wg0"); err == nil {
			r.WGRouteExists = strings.TrimSpace(out) != ""
		}
	}

	// 4. TCPEstablished / TCPTimeWait: parse `ss -s`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-s"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "tcp:") || strings.Contains(lower, "estab") {
					// Parse "estab N" and "timewait N" patterns from the line.
					r.TCPEstablished = extractSSCount(line, "estab")
					r.TCPTimeWait = extractSSCount(line, "timewait")
				}
			}
		}
	}

	// 5. TCPRetransRate: read /proc/net/snmp
	{
		if data, err := os.ReadFile("/proc/net/snmp"); err == nil {
			r.TCPRetransRate = parseTCPRetransRate(string(data))
		}
	}

	// 6. ListeningPorts: ss -tlnp (TCP) + ss -ulnp (UDP)
	{
		seen := make(map[string]bool)

		ctx1, cancel1 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel1()
		if out, err := runCmd(ctx1, "ss", "-tlnp"); err == nil {
			for _, pi := range parseSSListening(out, "tcp") {
				key := strconv.Itoa(pi.Port) + "/" + pi.Proto
				if !seen[key] {
					seen[key] = true
					r.ListeningPorts = append(r.ListeningPorts, pi)
				}
			}
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()
		if out, err := runCmd(ctx2, "ss", "-ulnp"); err == nil {
			for _, pi := range parseSSListening(out, "udp") {
				key := strconv.Itoa(pi.Port) + "/" + pi.Proto
				if !seen[key] {
					seen[key] = true
					r.ListeningPorts = append(r.ListeningPorts, pi)
				}
			}
		}

		// Sort by port number for consistent output.
		sort.Slice(r.ListeningPorts, func(i, j int) bool {
			if r.ListeningPorts[i].Port != r.ListeningPorts[j].Port {
				return r.ListeningPorts[i].Port < r.ListeningPorts[j].Port
			}
			return r.ListeningPorts[i].Proto < r.ListeningPorts[j].Proto
		})
	}

	// 7. UFWActive: ufw status
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ufw", "status"); err == nil {
			r.UFWActive = strings.Contains(out, "Status: active")
		}
	}

	// 8. UFWRules: ufw status numbered
	if r.UFWActive {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ufw", "status", "numbered"); err == nil {
			r.UFWRules = parseUFWRules(out)
		}
	}

	return r
}

// extractSSCount finds a pattern like "estab 42" or "timewait 7" in an ss -s line.
func extractSSCount(line, keyword string) int {
	re := regexp.MustCompile(keyword + `\s+(\d+)`)
	m := re.FindStringSubmatch(line)
	if len(m) >= 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 0
}

// parseTCPRetransRate parses /proc/net/snmp content to compute
// RetransSegs / OutSegs * 100.
//
// The file has paired lines: a header line followed by a values line.
// We look for the "Tcp:" header and extract RetransSegs and OutSegs.
func parseTCPRetransRate(data string) float64 {
	lines := strings.Split(data, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "Tcp:") {
			continue
		}
		header := strings.Fields(lines[i])
		values := strings.Fields(lines[i+1])
		if !strings.HasPrefix(lines[i+1], "Tcp:") || len(header) != len(values) {
			continue
		}

		var outSegs, retransSegs float64
		for j, h := range header {
			switch h {
			case "OutSegs":
				if v, err := strconv.ParseFloat(values[j], 64); err == nil {
					outSegs = v
				}
			case "RetransSegs":
				if v, err := strconv.ParseFloat(values[j], 64); err == nil {
					retransSegs = v
				}
			}
		}
		if outSegs > 0 {
			return retransSegs / outSegs * 100
		}
		return 0
	}
	return 0
}

// parseSSListening parses the output of `ss -tlnp` or `ss -ulnp` to extract
// port numbers and process names.
func parseSSListening(output, proto string) []PortInfo {
	var ports []PortInfo
	processRe := regexp.MustCompile(`users:\(\("([^"]+)"`)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// Skip header and empty lines.
		if line == "" || strings.HasPrefix(line, "State") || strings.HasPrefix(line, "Netid") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// The local address:port is typically the 4th field (index 3) for ss -tlnp
		// or the 5th field (index 4) for some formats. We look for a field with ":PORT".
		localAddr := ""
		for _, f := range fields {
			if strings.Contains(f, ":") && !strings.HasPrefix(f, "users:") {
				// Could be *:port, 0.0.0.0:port, [::]:port, 127.0.0.1:port, etc.
				if idx := strings.LastIndex(f, ":"); idx >= 0 {
					portStr := f[idx+1:]
					if _, err := strconv.Atoi(portStr); err == nil {
						localAddr = f
						break
					}
				}
			}
		}

		if localAddr == "" {
			continue
		}

		idx := strings.LastIndex(localAddr, ":")
		if idx < 0 {
			continue
		}
		portStr := localAddr[idx+1:]
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}

		process := ""
		if m := processRe.FindStringSubmatch(line); len(m) >= 2 {
			process = m[1]
		}

		ports = append(ports, PortInfo{
			Port:    port,
			Proto:   proto,
			Process: process,
		})
	}
	return ports
}

// parseUFWRules extracts rule lines from `ufw status numbered` output.
// Skips the header lines (Status, To, ---, blank lines).
func parseUFWRules(output string) []string {
	var rules []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Rule lines start with "[ N]" pattern.
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			rules = append(rules, line)
		}
	}
	return rules
}
