package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// collectNamespaces discovers deployed namespaces and checks health of their
// per-namespace services (RQLite, Olric, Gateway).
func collectNamespaces() []NamespaceReport {
	namespaces := discoverNamespaces()
	if len(namespaces) == 0 {
		return nil
	}

	var reports []NamespaceReport
	for _, ns := range namespaces {
		reports = append(reports, collectNamespaceReport(ns))
	}
	return reports
}

type nsInfo struct {
	name     string
	portBase int
}

// discoverNamespaces finds deployed namespaces by looking for systemd service units
// and/or the filesystem namespace directory.
func discoverNamespaces() []nsInfo {
	var result []nsInfo
	seen := make(map[string]bool)

	// Strategy 1: Glob for orama-deploy-*-rqlite.service files.
	matches, _ := filepath.Glob("/etc/systemd/system/orama-deploy-*-rqlite.service")
	for _, path := range matches {
		base := filepath.Base(path)
		// Extract namespace name: orama-deploy-<name>-rqlite.service
		name := strings.TrimPrefix(base, "orama-deploy-")
		name = strings.TrimSuffix(name, "-rqlite.service")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		portBase := parsePortBaseFromUnit(path)
		if portBase > 0 {
			result = append(result, nsInfo{name: name, portBase: portBase})
		}
	}

	// Strategy 2: Check filesystem for any namespaces not found via systemd.
	nsDir := "/opt/orama/.orama/data/namespaces"
	entries, err := os.ReadDir(nsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			name := entry.Name()
			seen[name] = true

			// Try to find the port base from a corresponding service unit.
			unitPath := fmt.Sprintf("/etc/systemd/system/orama-deploy-%s-rqlite.service", name)
			portBase := parsePortBaseFromUnit(unitPath)
			if portBase > 0 {
				result = append(result, nsInfo{name: name, portBase: portBase})
			}
		}
	}

	return result
}

// parsePortBaseFromUnit reads a systemd unit file and extracts the port base
// from ExecStart arguments or environment variables.
//
// It looks for patterns like:
//   - "-http-addr localhost:PORT" or "-http-addr 0.0.0.0:PORT" in ExecStart
//   - "PORT_BASE=NNNN" in environment files
//   - Any port number that appears to be the RQLite HTTP port (the base port)
func parsePortBaseFromUnit(unitPath string) int {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return 0
	}
	content := string(data)

	// Look for -http-addr with a port number in ExecStart line.
	httpAddrRe := regexp.MustCompile(`-http-addr\s+\S+:(\d+)`)
	if m := httpAddrRe.FindStringSubmatch(content); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil {
			return port
		}
	}

	// Look for a port in -addr or -http flags.
	addrRe := regexp.MustCompile(`(?:-addr|-http)\s+\S*:(\d+)`)
	if m := addrRe.FindStringSubmatch(content); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil {
			return port
		}
	}

	// Look for PORT_BASE environment variable in EnvironmentFile or Environment= directives.
	portBaseRe := regexp.MustCompile(`PORT_BASE=(\d+)`)
	if m := portBaseRe.FindStringSubmatch(content); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil {
			return port
		}
	}

	// Check referenced EnvironmentFile for PORT_BASE.
	envFileRe := regexp.MustCompile(`EnvironmentFile=(.+)`)
	if m := envFileRe.FindStringSubmatch(content); len(m) >= 2 {
		envPath := strings.TrimSpace(m[1])
		envPath = strings.TrimPrefix(envPath, "-") // optional prefix means "ignore if missing"
		if envData, err := os.ReadFile(envPath); err == nil {
			if m2 := portBaseRe.FindStringSubmatch(string(envData)); len(m2) >= 2 {
				if port, err := strconv.Atoi(m2[1]); err == nil {
					return port
				}
			}
		}
	}

	return 0
}

// collectNamespaceReport checks the health of services for a single namespace.
func collectNamespaceReport(ns nsInfo) NamespaceReport {
	r := NamespaceReport{
		Name:     ns.name,
		PortBase: ns.portBase,
	}

	// 1. RQLiteUp + RQLiteState: GET http://localhost:<port_base>/status
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		url := fmt.Sprintf("http://localhost:%d/status", ns.portBase)
		if body, err := httpGet(ctx, url); err == nil {
			r.RQLiteUp = true

			var status map[string]interface{}
			if err := json.Unmarshal(body, &status); err == nil {
				r.RQLiteState = getNestedString(status, "store", "raft", "state")
			}
		}
	}

	// 2. RQLiteReady: GET http://localhost:<port_base>/readyz
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		url := fmt.Sprintf("http://localhost:%d/readyz", ns.portBase)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				r.RQLiteReady = resp.StatusCode == http.StatusOK
			}
		}
	}

	// 3. OlricUp: check if port_base+2 is listening
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
			r.OlricUp = portIsListening(out, ns.portBase+2)
		}
	}

	// 4. GatewayUp + GatewayStatus: GET http://localhost:<port_base+4>/v1/health
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		url := fmt.Sprintf("http://localhost:%d/v1/health", ns.portBase+4)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				r.GatewayUp = true
				r.GatewayStatus = resp.StatusCode
			}
		}
	}

	return r
}
