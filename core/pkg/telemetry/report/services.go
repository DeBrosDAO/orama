package report

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var coreServices = []string{
	"orama-node",
	"orama-namespace-olric@index",
	"orama-namespace-ipfs@index",
	"orama-namespace-ipfs-cluster@index",
	"orama-namespace-vault@index",
	"orama-namespace-tor@index",
	caddyUnit,
	"orama-namespace-wireguard@index",
	coreDNSUnit,
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

	info.RestartLoopRisk = restartLoopRisk(info.NRestarts, info.ActiveSinceSec)

	return info
}

// restartLoopThreshold is how many restarts make a unit suspicious, and
// restartLoopUptime is how long it must have stayed up to be considered
// recovered rather than looping.
const (
	restartLoopThreshold = 3
	restartLoopUptime    = 300
)

// restartLoopRisk reports whether a unit looks like it is crash-looping.
//
// activeSinceSec is 0 when systemd reports ActiveEnterTimestamp as "n/a" —
// which is the case for a unit that has never reached active at all. That is
// the worst kind of loop, not the absence of one: a unit whose binary is
// missing or whose config will not parse restarts forever in
// `activating (auto-restart)`. It used to land in `failed` and raise a critical
// alert; since the supervised units carry StartLimitIntervalSec=0 (see "Unit
// restart policy" in docs/ARCHITECTURE.md) nothing parks them any more, so the
// restart counter is the only signal left and this is where it has to be read.
func restartLoopRisk(nRestarts int, activeSinceSec int64) bool {
	if nRestarts <= restartLoopThreshold {
		return false
	}
	return activeSinceSec == 0 || activeSinceSec < restartLoopUptime
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

// collectFailedUnits runs `systemctl --failed` and returns the failed units' names.
func collectFailedUnits(ctx context.Context) []string {
	out, err := runCmd(ctx, "systemctl", "--failed", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		return nil
	}
	return parseFailedUnits(out)
}

// parseFailedUnits takes the unit name from each line of `systemctl --failed
// --no-legend`. Without --plain, systemd prints a status glyph as a field of
// its own ("● name.service loaded failed failed …"; other versions use × or
// ○), and the name is the first field holding a letter or digit, with any of
// those glyphs joined to it trimmed (and nothing else: "-.mount" is a name). Taking the first field, as this used to, read the
// bullet, trimmed it to nothing and dropped every failed unit, so the "Failed
// systemd unit" alert could never fire.
// statusGlyphs are the unit-state markers systemd prints before a name.
const statusGlyphs = "●○×*"

func parseFailedUnits(out string) []string {
	var units []string
	for _, line := range strings.Split(out, "\n") {
		for _, field := range strings.Fields(line) {
			name := strings.TrimLeft(field, statusGlyphs)
			if strings.IndexFunc(name, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
				units = append(units, name)
				break
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
