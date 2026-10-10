package report

import (
	"bytes"
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

// Handle collects this node's health data and writes it as JSON.
//
// compact selects one line rather than indented output. The output is JSON
// either way: `orama status --ssh` parses it over SSH, and a person reading it
// on the node wants it indented. The parameter used to be called jsonFlag,
// which made the command's flag read as "output JSON" when it only chose the
// formatting — and since it defaulted to true, setting it changed nothing.
func Handle(compact bool, version string) error {
	rpt := Collect(version)
	enc := json.NewEncoder(os.Stdout)
	if !compact {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(rpt)
}

// Collect gathers this node's health data. Every collector runs in parallel
// and a collector that panics is recorded in Errors rather than taking the
// report down with it. Each collector bounds its own calls with timeouts. It
// needs root for full data: the cluster gateway gets
// it through the privileged helper (privhelper.ToolNodeReport).
func Collect(version string) *NodeReport {
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
	for _, c := range collectors(rpt) {
		safeGo(&wg, c.name, c.run)
	}
	wg.Wait()

	// An unreadable kernel log leaves the OOM count unknown; surface it.
	for _, msg := range systemErrors(rpt.System) {
		addError(msg)
	}

	// Populate top-level WireGuard IP from the WireGuard collector result.
	if rpt.WireGuard != nil && rpt.WireGuard.WgIP != "" {
		rpt.WGIP = rpt.WireGuard.WgIP
	}

	rpt.CollectMS = time.Since(start).Milliseconds()
	return rpt
}

// systemErrors lists what the system collector could not read.
func systemErrors(s *SystemReport) []string {
	if s == nil || s.OOMKillsError == "" {
		return nil
	}
	return []string{s.OOMKillsError}
}

// collector is one section of the report.
type collector struct {
	name string
	run  func()
}

// collectors lists every section and where its result goes.
func collectors(rpt *NodeReport) []collector {
	return []collector{
		{"system", func() { rpt.System = collectSystem() }},
		{"services", func() { rpt.Services = collectServices() }},
		{"rqlite", func() { rpt.RQLite = collectRQLite() }},
		{"olric", func() { rpt.Olric = collectOlric() }},
		{"ipfs", func() { rpt.IPFS = collectIPFS() }},
		{"vault", func() { rpt.Vault = collectVault() }},
		{"gateway", func() { rpt.Gateway = collectGateway() }},
		{"wireguard", func() { rpt.WireGuard = collectWireGuard() }},
		{"dns", func() {
			// Only collect DNS info if this node runs CoreDNS.
			if _, err := os.Stat("/etc/coredns"); err == nil {
				rpt.DNS = collectDNS()
			}
		}},
		{"tor", func() { rpt.Tor = collectTor() }},
		{"network", func() { rpt.Network = collectNetwork() }},
		{"processes", func() { rpt.Processes = collectProcesses() }},
		{"namespaces", func() { rpt.Namespaces = collectNamespaces() }},
		{"deployments", func() { rpt.Deployments = collectDeployments() }},
		{"serverless", func() { rpt.Serverless = collectServerless() }},
		{"chain", func() { rpt.Chain = collectChain() }},
		{"global", func() { rpt.Global = collectGlobal() }},
		{"update", func() { rpt.Update = collectUpdate() }},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const (
	// localCommandTimeout bounds one external command a collector runs.
	localCommandTimeout = 4 * time.Second
	// localHTTPTimeout bounds one request httpGet makes.
	localHTTPTimeout = 3 * time.Second
	// maxLocalResponseBytes caps every response body the report reads from a
	// loopback service. The collector runs as root, and a tenant process can
	// bind a stopped service's loopback port and answer with an endless body;
	// nothing a local service legitimately answers comes near this.
	maxLocalResponseBytes = 2 << 20
)

// commandStdout runs a command and returns its trimmed stdout. A non-zero
// exit that still printed stdout is that text, not an error: systemd
// is-active prints "failed" and exits 3.
func commandStdout(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	text := strings.TrimSpace(stdout.String())
	if err != nil && text == "" {
		return "", err
	}
	return text, nil
}

// runCmd executes an external command with localCommandTimeout and returns
// its stdout as a trimmed string.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// httpGet performs an HTTP GET request with localHTTPTimeout and returns the
// response body bytes, at most maxLocalResponseBytes of them.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, localHTTPTimeout)
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

	body, err := readLocalBody(resp.Body, url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return body, nil
}

// readLocalBody reads a loopback response body. A body over
// maxLocalResponseBytes is an error naming url, not a truncated read.
func readLocalBody(body io.Reader, url string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxLocalResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the response from %s: %w", url, err)
	}
	if len(data) > maxLocalResponseBytes {
		return nil, fmt.Errorf("the response from %s is over the %d-byte limit", url, maxLocalResponseBytes)
	}
	return data, nil
}

// drainLocalBody discards up to maxLocalResponseBytes of a body whose content
// is not needed, so the connection can be reused without reading an endless
// body to its end.
func drainLocalBody(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxLocalResponseBytes))
}
