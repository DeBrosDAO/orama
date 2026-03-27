package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Handle is the main entry point for `orama node report`.
// It collects system, service, and component information in parallel,
// then outputs the full NodeReport as JSON to stdout.
func Handle(jsonFlag bool, version string) error {
	start := time.Now()

	rpt := &NodeReport{
		Timestamp: start.UTC(),
		Version:   version,
	}

	if h, err := os.Hostname(); err == nil {
		rpt.Hostname = h
	}

	var mu sync.Mutex
	addError := func(msg string) {
		mu.Lock()
		rpt.Errors = append(rpt.Errors, msg)
		mu.Unlock()
	}

	// safeGo launches a collector goroutine with panic recovery.
	safeGo := func(wg *sync.WaitGroup, name string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					addError(fmt.Sprintf("%s collector panicked: %v", name, r))
				}
			}()
			fn()
		}()
	}

	var wg sync.WaitGroup

	safeGo(&wg, "system", func() {
		rpt.System = collectSystem()
	})

	safeGo(&wg, "services", func() {
		rpt.Services = collectServices()
	})

	safeGo(&wg, "rqlite", func() {
		rpt.RQLite = collectRQLite()
	})

	safeGo(&wg, "olric", func() {
		rpt.Olric = collectOlric()
	})

	safeGo(&wg, "ipfs", func() {
		rpt.IPFS = collectIPFS()
	})

	safeGo(&wg, "vault", func() {
		rpt.Vault = collectVault()
	})

	safeGo(&wg, "gateway", func() {
		rpt.Gateway = collectGateway()
	})

	safeGo(&wg, "wireguard", func() {
		rpt.WireGuard = collectWireGuard()
	})

	safeGo(&wg, "dns", func() {
		// Only collect DNS info if this node runs CoreDNS.
		if _, err := os.Stat("/etc/coredns"); err == nil {
			rpt.DNS = collectDNS()
		}
	})

	safeGo(&wg, "anyone", func() {
		rpt.Anyone = collectAnyone()
	})

	safeGo(&wg, "network", func() {
		rpt.Network = collectNetwork()
	})

	safeGo(&wg, "processes", func() {
		rpt.Processes = collectProcesses()
	})

	safeGo(&wg, "namespaces", func() {
		rpt.Namespaces = collectNamespaces()
	})

	safeGo(&wg, "deployments", func() {
		rpt.Deployments = collectDeployments()
	})

	safeGo(&wg, "serverless", func() {
		rpt.Serverless = collectServerless()
	})

	wg.Wait()

	// Populate top-level WireGuard IP from the WireGuard collector result.
	if rpt.WireGuard != nil && rpt.WireGuard.WgIP != "" {
		rpt.WGIP = rpt.WireGuard.WgIP
	}

	rpt.CollectMS = time.Since(start).Milliseconds()

	enc := json.NewEncoder(os.Stdout)
	if !jsonFlag {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(rpt)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// runCmd executes an external command with a 4-second timeout and returns its
// combined stdout as a trimmed string.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// httpGet performs an HTTP GET request with a 3-second timeout and returns the
// response body bytes.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return body, nil
}
