package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

	// Strategy 1: Glob for orama-namespace-rqlite@*.service files.
	matches, _ := filepath.Glob("/etc/systemd/system/orama-namespace-rqlite@*.service")
	for _, path := range matches {
		base := filepath.Base(path)
		// Extract namespace name: orama-namespace-rqlite@<name>.service
		name := strings.TrimPrefix(base, "orama-namespace-rqlite@")
		name = strings.TrimSuffix(name, ".service")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		portBase := parsePortFromEnvFile(name)
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

			portBase := parsePortFromEnvFile(name)
			if portBase > 0 {
				result = append(result, nsInfo{name: name, portBase: portBase})
			}
		}
	}

	return result
}

// parsePortFromEnvFile reads the RQLite env file for a namespace and extracts
// the HTTP port from HTTP_ADDR (e.g. "0.0.0.0:14001").
func parsePortFromEnvFile(namespace string) int {
	envPath := fmt.Sprintf("/opt/orama/.orama/data/namespaces/%s/rqlite.env", namespace)
	data, err := os.ReadFile(envPath)
	if err != nil {
		return 0
	}

	httpAddrRe := regexp.MustCompile(`HTTP_ADDR=\S+:(\d+)`)
	if m := httpAddrRe.FindStringSubmatch(string(data)); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil {
			return port
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

	// 5. SFUUp: check if namespace SFU systemd service is active (optional)
	r.SFUUp = isNamespaceServiceActive("sfu", ns.name)

	// 6. TURNUp: check if namespace TURN systemd service is active (optional)
	r.TURNUp = isNamespaceServiceActive("turn", ns.name)

	return r
}

// isNamespaceServiceActive checks if a namespace service is provisioned and active.
// Returns false if the service is not provisioned (no env file) or not running.
func isNamespaceServiceActive(serviceType, namespace string) bool {
	// Only check if the service was provisioned (env file exists)
	envFile := fmt.Sprintf("/opt/orama/.orama/data/namespaces/%s/%s.env", namespace, serviceType)
	if _, err := os.Stat(envFile); err != nil {
		return false // not provisioned
	}

	svcName := fmt.Sprintf("orama-namespace-%s@%s", serviceType, namespace)
	cmd := exec.Command("systemctl", "is-active", "--quiet", svcName)
	return cmd.Run() == nil
}
