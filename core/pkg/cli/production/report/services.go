package report

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var coreServices = []string{
	"orama-node",
	"orama-namespace-olric@index",
	"orama-namespace-ipfs@index",
	"orama-namespace-ipfs-cluster@index",
	"orama-namespace-vault@index",
	"orama-namespace-anyone-client@index",
	"orama-namespace-caddy@index",
	"orama-namespace-wireguard@index",
	"coredns",
}

func collectServices() *ServicesReport {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := &ServicesReport{}

	// Collect core services.
	for _, name := range coreServices {
		info := collectServiceInfo(ctx, name)
		report.Services = append(report.Services, info)
	}

	// Discover namespace services (orama-deploy-*.service).
	nsServices := discoverNamespaceServices()
	for _, name := range nsServices {
		info := collectServiceInfo(ctx, name)
		report.Services = append(report.Services, info)
	}

	// Collect failed units.
	report.FailedUnits = collectFailedUnits(ctx)

	return report
}

func collectServiceInfo(ctx context.Context, name string) ServiceInfo {
	info := ServiceInfo{Name: name}

	// Get all properties in a single systemctl show call.
	out, err := runCmd(ctx, "systemctl", "show", name,
		"--property=ActiveState,SubState,NRestarts,ActiveEnterTimestamp,MemoryCurrent,CPUUsageNSec,MainPID")
	if err != nil {
		info.ActiveState = "unknown"
		info.SubState = "unknown"
		return info
	}

	props := parseProperties(out)

	info.ActiveState = props["ActiveState"]
	info.SubState = props["SubState"]
	info.NRestarts = parseInt(props["NRestarts"])
	info.MainPID = parseInt(props["MainPID"])
	info.MemoryCurrentMB = parseMemoryMB(props["MemoryCurrent"])
	info.CPUUsageNSec = parseInt64(props["CPUUsageNSec"])

	// Calculate uptime from ActiveEnterTimestamp.
	if ts := props["ActiveEnterTimestamp"]; ts != "" && ts != "n/a" {
		info.ActiveSinceSec = parseActiveSince(ts)
	}

	// Check if service is enabled.
	enabledOut, err := runCmd(ctx, "systemctl", "is-enabled", name)
	if err == nil && strings.TrimSpace(enabledOut) == "enabled" {
		info.Enabled = true
	}

	// Restart loop detection: restarted more than 3 times and running for less than 5 minutes.
	info.RestartLoopRisk = info.NRestarts > 3 && info.ActiveSinceSec > 0 && info.ActiveSinceSec < 300

	return info
}

// parseProperties parses "Key=Value" lines from systemctl show output into a map.
func parseProperties(output string) map[string]string {
	props := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := line[:idx]
		value := line[idx+1:]
		props[key] = value
	}
	return props
}

// parseMemoryMB converts a MemoryCurrent value (bytes as uint64, "[not set]", or "infinity") to MB.
func parseMemoryMB(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "[not set]" || s == "infinity" {
		return 0
	}
	bytes, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return int(bytes / (1024 * 1024))
}

// parseActiveSince parses an ActiveEnterTimestamp like "Fri 2024-01-05 10:30:00 UTC"
// and returns the number of seconds elapsed since that time.
func parseActiveSince(ts string) int64 {
	// systemctl outputs timestamps in the form: "Day YYYY-MM-DD HH:MM:SS TZ"
	// e.g. "Fri 2024-01-05 10:30:00 UTC"
	layouts := []string{
		"Mon 2006-01-02 15:04:05 MST",
		"Mon 2006-01-02 15:04:05 -0700",
	}
	ts = strings.TrimSpace(ts)
	for _, layout := range layouts {
		t, err := time.Parse(layout, ts)
		if err == nil {
			sec := int64(time.Since(t).Seconds())
			if sec < 0 {
				return 0
			}
			return sec
		}
	}
	return 0
}

func parseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "[not set]" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "[not set]" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// collectFailedUnits runs `systemctl --failed` and extracts unit names from the first column.
func collectFailedUnits(ctx context.Context) []string {
	out, err := runCmd(ctx, "systemctl", "--failed", "--no-legend", "--no-pager")
	if err != nil {
		return nil
	}

	var units []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			// First column may have a bullet prefix; strip common markers.
			unit := strings.TrimLeft(fields[0], "●* ")
			if unit != "" {
				units = append(units, unit)
			}
		}
	}
	return units
}

// discoverNamespaceServices finds orama-namespace-*@*.service files in /etc/systemd/system
// and returns the service names (without the .service suffix path).
func discoverNamespaceServices() []string {
	matches, err := filepath.Glob("/etc/systemd/system/orama-namespace-*@*.service")
	if err != nil || len(matches) == 0 {
		return nil
	}

	var services []string
	for _, path := range matches {
		base := filepath.Base(path)
		// Strip the .service suffix to get the unit name.
		name := strings.TrimSuffix(base, ".service")
		services = append(services, name)
	}
	return services
}
