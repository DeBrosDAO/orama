package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// collectNamespaces discovers deployed namespaces and checks health of their
// per-namespace services (RQLite, Olric, Gateway).
func collectNamespaces() []NamespaceReport {
	namespaces := discoverNamespaces()
	if len(namespaces) == 0 {
		return nil
	}

	// Every namespace rqlited runs with -auth and the cluster-wide
	// credentials, which node.yaml carries.
	creds, credsErr := rqlite.LocalNodeEndpoint()

	var reports []NamespaceReport
	for _, ns := range namespaces {
		if ns.addrErr != nil {
			// Without an address there is no port block either, so there is
			// nothing else to probe for this namespace.
			reports = append(reports, NamespaceReport{Name: ns.name, RQLiteError: ns.addrErr.Error()})
			continue
		}
		rqliteBase, rqliteErr := namespaceRQLiteBase(ns, creds, credsErr)
		reports = append(reports, collectNamespaceReport(ns, rqliteBase, rqliteErr))
	}
	return reports
}

type nsInfo struct {
	name       string
	portBase   int
	rqliteAddr string // where the instance is reached (rqlite.InstanceAddrFromEnv)
	addrErr    error  // why rqliteAddr could not be read
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

		if ns, ok := namespaceInfo(name); ok {
			result = append(result, ns)
		}
	}

	// Strategy 2: Check filesystem for any namespaces not found via systemd.
	entries, err := os.ReadDir(config.ProductionNamespacesDataDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			name := entry.Name()
			seen[name] = true

			if ns, ok := namespaceInfo(name); ok {
				result = append(result, ns)
			}
		}
	}

	return result
}

// namespaceInfo reads a namespace's rqlite instance from its rqlite.env. ok is
// false when the namespace has no rqlite instance on this node. The port block
// base is the rqlite HTTP port.
func namespaceInfo(name string) (nsInfo, bool) {
	envFile := rqlite.InstanceEnvFile(unitenv.Dir, name)
	addr, ok, err := rqlite.InstanceAddrFromEnv(envFile)
	if !ok {
		return nsInfo{}, false
	}
	if err != nil {
		return nsInfo{name: name, addrErr: err}, true
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nsInfo{name: name, addrErr: fmt.Errorf("%s: rqlite address %q: %w", envFile, addr, err)}, true
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nsInfo{name: name, addrErr: fmt.Errorf("%s: rqlite address %q has an invalid port", envFile, addr)}, true
	}
	return nsInfo{name: name, portBase: port, rqliteAddr: addr}, true
}

// namespaceRQLiteBase is the namespace instance's base URL: its address with
// the cluster credentials, which ride in the URL (net/http sends
// them as basic auth and strips them from its errors).
func namespaceRQLiteBase(ns nsInfo, creds rqlite.Endpoint, credsErr error) (string, error) {
	if credsErr != nil {
		return "", credsErr
	}
	ep, err := rqlite.NewEndpoint(ns.rqliteAddr, creds.Username, creds.Password)
	if err != nil {
		return "", err
	}
	return ep.CredentialedURL(), nil
}

// namespaceGatewayPortOffset is the gateway's offset in a namespace port block
// (0 rqlite HTTP, 1 rqlite raft, 2 olric HTTP, 3 olric memberlist, 4 gateway).
const namespaceGatewayPortOffset = 4

// namespaceGatewayHealthURL is the namespace gateway's /v1/health. A tenant
// gateway binds this node's WireGuard IP, not every interface (chg-387), so it
// is probed on the host its rqlite instance is reached on — the same WireGuard
// IP — never on localhost, where it does not listen. The index gateway binds
// every interface, so the WireGuard IP reaches it too.
func namespaceGatewayHealthURL(ns nsInfo) (string, error) {
	host, _, err := net.SplitHostPort(ns.rqliteAddr)
	if err != nil || host == "" {
		return "", fmt.Errorf("namespace %s has no address to probe its gateway on", ns.name)
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(ns.portBase+namespaceGatewayPortOffset)) + "/v1/health", nil
}

// collectNamespaceReport checks the health of services for a single namespace.
// rqliteBase is empty when the instance cannot be addressed (rqliteErr says
// why); the rqlite checks are then reported as down.
func collectNamespaceReport(ns nsInfo, rqliteBase string, rqliteErr error) NamespaceReport {
	r := NamespaceReport{
		Name:     ns.name,
		PortBase: ns.portBase,
	}
	if rqliteErr != nil {
		r.RQLiteError = rqliteErr.Error()
	}

	// 1. RQLiteUp + RQLiteState: GET <rqlite>/status
	if rqliteBase != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if body, err := httpGet(ctx, rqliteBase+"/status"); err == nil {
			r.RQLiteUp = true

			var status map[string]interface{}
			if err := json.Unmarshal(body, &status); err == nil {
				r.RQLiteState = getNestedString(status, "store", "raft", "state")
			}
		}
	}

	// 2. RQLiteReady: GET <rqlite>/readyz
	if rqliteBase != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rqliteBase+"/readyz", nil)
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

	// 4. GatewayUp + GatewayStatus: GET <gateway>/v1/health, where the
	// gateway binds (namespaceGatewayHealthURL).
	if url, err := namespaceGatewayHealthURL(ns); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

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

	// 6. TURNUp: TURN is host-level since bugboard #283 part 2 — one shared
	// server per node serves every namespace allocated there. So "is TURN up for
	// this namespace" is the shared unit running AND this namespace being one of
	// the tenants it is configured to serve. Checking the old per-namespace unit
	// here would report turn_up false on every node forever, silently removing
	// TURN from the report the rolling-upgrade protocol depends on.
	r.TURNUp = hostTURNServesNamespace(ns.name)

	return r
}

// isNamespaceServiceActive checks if a namespace service is provisioned and active.
// Returns false if the service is not provisioned (no env file) or not running.
func isNamespaceServiceActive(serviceType, namespace string) bool {
	// Only check if the service was provisioned (env file exists)
	envFile := unitenv.Path(unitenv.Dir, namespace, serviceType)
	if _, err := os.Stat(envFile); err != nil {
		return false // not provisioned
	}

	svcName := fmt.Sprintf("orama-namespace-%s@%s", serviceType, namespace)
	cmd := exec.Command("systemctl", "is-active", "--quiet", svcName)
	return cmd.Run() == nil
}

// hostTURNServesNamespace reports whether this host's shared TURN server is
// running AND lists the namespace as a tenant (bugboard #283 part 2).
//
// Both halves matter: the unit being up says nothing about whether it relays for
// THIS namespace, and a namespace listed in a config no process is reading has
// no relay at all.
func hostTURNServesNamespace(namespace string) bool {
	data, err := os.ReadFile(hostTURNConfigPath)
	if err != nil {
		return false
	}
	cfg, perr := turn.ParseConfig(data)
	if perr != nil {
		return false
	}
	if _, served := cfg.TenantSecret(namespace); !served {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", systemd.HostTURNServiceName).Run() == nil
}

// hostTURNConfigPath is the TURN_CONFIG the shared TURN unit reads.
var hostTURNConfigPath = constants.HostTURNConfigPath(install.OramaDir)
