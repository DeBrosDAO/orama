package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	xcaddyRepo = "github.com/caddyserver/xcaddy/cmd/xcaddy@latest"
)

// CaddyInstaller handles Caddy installation with custom DNS module
type CaddyInstaller struct {
	*BaseInstaller
	version   string
	oramaHome string
	dnsModule string // Path to the orama DNS module source

	// withNtfy, when set, causes generateCaddyfile to emit a reverse-
	// proxy block for `push.<dnsZone>` → localhost:<NtfyListenPort>.
	// Enabled per-node via EnableNtfyProxy. Feature #72.
	withNtfy     bool
	ntfyHostname string // e.g. "push.dbrs.space" — fully-qualified public host

	// behindSNIRouter, when set, moves Caddy's HTTPS listener off :443 to
	// CaddyHTTPSPortBehindSNI so the orama-sni-router can own :443 and forward
	// TLS by SNI (feat-124, stealth TURN). Enabled per-node via
	// EnableSNIRouterMode. Plain HTTP (:80) is unaffected. When false the
	// generated Caddyfile is byte-identical to the pre-feature output.
	behindSNIRouter bool
}

// CaddyHTTPSPortBehindSNI is the port Caddy binds for HTTPS when the node runs
// behind the SNI router (which owns :443). 8443 matches the sni-router config's
// caddy fallback backend (127.0.0.1:8443) and the plan doc.
const CaddyHTTPSPortBehindSNI = 8443

// NewCaddyInstaller creates a new Caddy installer
func NewCaddyInstaller(arch string, logWriter io.Writer, oramaHome string) *CaddyInstaller {
	return &CaddyInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       constants.CaddyVersion,
		oramaHome:     oramaHome,
		dnsModule:     filepath.Join(oramaHome, "src", "pkg", "caddy", "dns", "orama"),
	}
}

// EnableNtfyProxy tells the Caddy installer to emit a reverse-proxy
// block for the self-hosted ntfy server (feature #72). hostname is the
// public fully-qualified domain — e.g. "push.dbrs.space" — that Caddy
// will obtain a Let's Encrypt cert for and route to the local ntfy
// server on NtfyListenPort.
//
// Must be called BEFORE Configure so the generated Caddyfile includes
// the block.
func (ci *CaddyInstaller) EnableNtfyProxy(hostname string) {
	ci.withNtfy = true
	ci.ntfyHostname = hostname
}

// EnableSNIRouterMode tells the Caddy installer to bind HTTPS on
// CaddyHTTPSPortBehindSNI (8443) instead of :443, freeing :443 for the
// orama-sni-router (feat-124). Plain HTTP on :80 is left untouched. Must be
// called BEFORE Configure so the generated Caddyfile picks up the global
// `https_port` option. A no-op when never called: the default Caddyfile keeps
// HTTPS on :443.
func (ci *CaddyInstaller) EnableSNIRouterMode() {
	ci.behindSNIRouter = true
}

// IsInstalled checks if Caddy with orama DNS module is already installed
func (ci *CaddyInstaller) IsInstalled() bool {
	caddyPath := "/usr/bin/caddy"
	if _, err := os.Stat(caddyPath); os.IsNotExist(err) {
		return false
	}

	// Verify it has the orama DNS module
	cmd := exec.Command(caddyPath, "list-modules")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return containsLine(string(output), "dns.providers.orama")
}

// Install builds and installs Caddy with the custom orama DNS module
func (ci *CaddyInstaller) Install() error {
	if ci.IsInstalled() {
		fmt.Fprintf(ci.logWriter, "  ✓ Caddy with orama DNS module already installed\n")
		return nil
	}

	fmt.Fprintf(ci.logWriter, "  Building Caddy with orama DNS module...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to build Caddy. Please install Go first")
	}

	goPath := os.Getenv("PATH") + ":/usr/local/go/bin"
	buildDir := "/tmp/caddy-build"

	// Clean up any previous build
	os.RemoveAll(buildDir)
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("failed to create build directory: %w", err)
	}
	defer os.RemoveAll(buildDir)

	// Install xcaddy if not available
	if _, err := exec.LookPath("xcaddy"); err != nil {
		fmt.Fprintf(ci.logWriter, "    Installing xcaddy...\n")
		cmd := exec.Command("go", "install", xcaddyRepo)
		cmd.Env = append(os.Environ(), "PATH="+goPath, "GOBIN=/usr/local/bin", "GOPROXY=https://proxy.golang.org|direct", "GONOSUMDB=*")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to install xcaddy: %w\n%s", err, string(output))
		}
	}

	// Create the orama DNS module in build directory
	fmt.Fprintf(ci.logWriter, "    Creating orama DNS module...\n")
	moduleDir := filepath.Join(buildDir, "caddy-dns-orama")
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		return fmt.Errorf("failed to create module directory: %w", err)
	}

	// Write the provider.go file
	providerCode := ci.generateProviderCode()
	if err := os.WriteFile(filepath.Join(moduleDir, "provider.go"), []byte(providerCode), 0644); err != nil {
		return fmt.Errorf("failed to write provider.go: %w", err)
	}

	// Write go.mod
	goMod := ci.generateGoMod()
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	// Run go mod tidy
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = moduleDir
	tidyCmd.Env = append(os.Environ(), "PATH="+goPath, "GOPROXY=https://proxy.golang.org|direct", "GONOSUMDB=*")
	if output, err := tidyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run go mod tidy: %w\n%s", err, string(output))
	}

	// Build Caddy with xcaddy
	fmt.Fprintf(ci.logWriter, "    Building Caddy binary...\n")
	xcaddyPath := "/usr/local/bin/xcaddy"
	if _, err := os.Stat(xcaddyPath); os.IsNotExist(err) {
		xcaddyPath = "xcaddy" // Try PATH
	}

	buildCmd := exec.Command(xcaddyPath, "build",
		"v"+ci.version,
		"--with", "github.com/DeBrosOfficial/caddy-dns-orama="+moduleDir,
		"--output", filepath.Join(buildDir, "caddy"))
	buildCmd.Dir = buildDir
	buildCmd.Env = append(os.Environ(), "PATH="+goPath, "GOPROXY=https://proxy.golang.org|direct", "GONOSUMDB=*")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build Caddy: %w\n%s", err, string(output))
	}

	// Verify the binary has orama DNS module
	verifyCmd := exec.Command(filepath.Join(buildDir, "caddy"), "list-modules")
	output, err := verifyCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to verify Caddy binary: %w", err)
	}
	if !containsLine(string(output), "dns.providers.orama") {
		return fmt.Errorf("Caddy binary does not contain orama DNS module")
	}

	// Install the binary
	fmt.Fprintf(ci.logWriter, "    Installing Caddy binary...\n")
	srcBinary := filepath.Join(buildDir, "caddy")
	dstBinary := "/usr/bin/caddy"

	data, err := os.ReadFile(srcBinary)
	if err != nil {
		return fmt.Errorf("failed to read built binary: %w", err)
	}
	if err := os.WriteFile(dstBinary, data, 0755); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	fmt.Fprintf(ci.logWriter, "  ✓ Caddy with orama DNS module installed\n")
	return nil
}

// Configure creates Caddy configuration files.
// baseDomain is optional — if provided (and different from domain), Caddy will also
// serve traffic for the base domain and its wildcard (e.g., *.dbrs.space).
func (ci *CaddyInstaller) Configure(domain string, email string, acmeEndpoint string, baseDomain string) error {
	configDir := "/etc/caddy"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create Caddyfile
	caddyfile := ci.generateCaddyfile(domain, email, acmeEndpoint, baseDomain)
	if err := os.WriteFile(filepath.Join(configDir, "Caddyfile"), []byte(caddyfile), 0644); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	return nil
}

// generateProviderCode creates the orama DNS provider code
func (ci *CaddyInstaller) generateProviderCode() string {
	return `// Package orama implements a DNS provider for Caddy that uses the Orama Network
// gateway's internal ACME API for DNS-01 challenge validation.
package orama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/libdns/libdns"
)

func init() {
	caddy.RegisterModule(Provider{})
}

// Provider wraps the Orama DNS provider for Caddy.
type Provider struct {
	// Endpoint is the URL of the Orama gateway's ACME API
	// Default: http://localhost:<index-gateway>/v1/internal/acme
	Endpoint string ` + "`json:\"endpoint,omitempty\"`" + `
}

// CaddyModule returns the Caddy module information.
func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "dns.providers.orama",
		New: func() caddy.Module { return new(Provider) },
	}
}

// Provision sets up the module.
func (p *Provider) Provision(ctx caddy.Context) error {
	if p.Endpoint == "" {
		p.Endpoint = "` + fmt.Sprintf("http://localhost:%d/v1/internal/acme", constants.GatewayAPIPort) + `"
	}
	return nil
}

// UnmarshalCaddyfile parses the Caddyfile configuration.
func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "endpoint":
				if !d.NextArg() {
					return d.ArgErr()
				}
				p.Endpoint = d.Val()
			default:
				return d.Errf("unrecognized option: %s", d.Val())
			}
		}
	}
	return nil
}

// AppendRecords adds records to the zone. For ACME, this presents the challenge.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var added []libdns.Record

	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}

		fqdn := rr.Name + "." + zone

		payload := map[string]string{
			"fqdn":  fqdn,
			"value": rr.Data,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return added, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint+"/present", bytes.NewReader(body))
		if err != nil {
			return added, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return added, fmt.Errorf("failed to present challenge: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return added, fmt.Errorf("present failed with status %d", resp.StatusCode)
		}

		added = append(added, rec)
	}

	return added, nil
}

// DeleteRecords removes records from the zone. For ACME, this cleans up the challenge.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var deleted []libdns.Record

	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}

		fqdn := rr.Name + "." + zone

		payload := map[string]string{
			"fqdn":  fqdn,
			"value": rr.Data,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return deleted, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint+"/cleanup", bytes.NewReader(body))
		if err != nil {
			return deleted, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return deleted, fmt.Errorf("failed to cleanup challenge: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return deleted, fmt.Errorf("cleanup failed with status %d", resp.StatusCode)
		}

		deleted = append(deleted, rec)
	}

	return deleted, nil
}

// GetRecords returns the records in the zone. Not used for ACME.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	return nil, nil
}

// SetRecords sets the records in the zone. Not used for ACME.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return nil, nil
}

// Interface guards
var (
	_ caddy.Module          = (*Provider)(nil)
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
)
`
}

// generateGoMod creates the go.mod file for the module
func (ci *CaddyInstaller) generateGoMod() string {
	return `module github.com/DeBrosOfficial/caddy-dns-orama

go 1.22

require (
	github.com/caddyserver/caddy/v2 v2.` + constants.CaddyVersion[2:] + `
	github.com/libdns/libdns v1.1.0
)
`
}

// generateCaddyfile creates the Caddyfile configuration.
// If baseDomain is provided and different from domain, Caddy also serves
// the base domain and its wildcard (e.g., *.dbrs.space alongside *.node1.dbrs.space).
func (ci *CaddyInstaller) generateCaddyfile(domain, email, acmeEndpoint, baseDomain string) string {
	// Let's Encrypt via ACME DNS-01 challenge (no fallback to self-signed)
	tlsBlock := fmt.Sprintf(`    tls {
        issuer acme {
            dns orama {
                endpoint %s
            }
        }
    }`, acmeEndpoint)

	var sb strings.Builder
	// Caddy protocol restrictions:
	//   - HTTP/3 (QUIC) is disabled so Caddy doesn't bind UDP 443, which
	//     TURN needs for relay.
	//   - HTTP/2 is also disabled (bug #249). HTTP/2 forbids the
	//     `Connection: Upgrade` and `Upgrade: websocket` headers per
	//     RFC 7540 §8.1.2.2, so any WebSocket-upgrade request the
	//     client sends over an h2 connection arrives at Caddy with
	//     those headers stripped. Caddy then forwards a plain
	//     HTTP/1.1 GET to the backend gateway, which no longer
	//     recognises the request as a WS upgrade — its
	//     `isWebSocketUpgrade(r)` check fails and the
	//     query-string `?api_key=` / `?jwt=` WS-auth fallback is
	//     ignored, producing 401. RFC 8441 ("Bootstrapping WebSockets
	//     with HTTP/2") would fix this, but iOS RN and many other
	//     mobile WS libraries don't implement it. Until they do, h1
	//     is the only protocol that keeps WS auth working.
	//   - Cost: lose h2 multiplexing on regular HTTP traffic.
	//     Acceptable trade-off for an API gateway whose dominant
	//     workload is REST + WebSocket (neither benefits much from
	//     h2 stream multiplexing — REST is keep-alive over h1, and
	//     WS is single-connection by design).
	// When this node runs behind the SNI router (feat-124), move Caddy's HTTPS
	// listener off :443 to CaddyHTTPSPortBehindSNI via the `https_port` global
	// option. The sni-router owns :443 and forwards TLS by SNI to either a
	// namespace's TURNS listener or here (127.0.0.1:8443). Plain HTTP (:80) is
	// unchanged. When behindSNIRouter is false, no `https_port` line is emitted
	// and the Caddyfile is byte-identical to the pre-feature output.
	httpsPortOption := ""
	if ci.behindSNIRouter {
		httpsPortOption = fmt.Sprintf("    https_port %d\n", CaddyHTTPSPortBehindSNI)
	}
	sb.WriteString(fmt.Sprintf("{\n    email %s\n%s    servers {\n        protocols h1\n    }\n}\n", email, httpsPortOption))

	gw := fmt.Sprintf("localhost:%d", constants.GatewayAPIPort)

	// Node domain blocks (e.g., node1.dbrs.space, *.node1.dbrs.space)
	sb.WriteString(fmt.Sprintf("\n*.%s {\n%s\n    reverse_proxy %s\n}\n", domain, tlsBlock, gw))
	sb.WriteString(fmt.Sprintf("\n%s {\n%s\n    reverse_proxy %s\n}\n", domain, tlsBlock, gw))

	// Base domain blocks (e.g., dbrs.space, *.dbrs.space) — for app routing
	if baseDomain != "" && baseDomain != domain {
		sb.WriteString(fmt.Sprintf("\n*.%s {\n%s\n    reverse_proxy %s\n}\n", baseDomain, tlsBlock, gw))
		sb.WriteString(fmt.Sprintf("\n%s {\n%s\n    reverse_proxy %s\n}\n", baseDomain, tlsBlock, gw))
	}

	// HTTP blocks — serve traffic over plain HTTP so the gateway is reachable
	// even when TLS certificates are unavailable (e.g., Let's Encrypt rate limits).
	// Without these, Caddy auto-redirects HTTP→HTTPS for the named domain blocks above.
	sb.WriteString(fmt.Sprintf("\nhttp://*.%s {\n    reverse_proxy %s\n}\n", domain, gw))
	sb.WriteString(fmt.Sprintf("\nhttp://%s {\n    reverse_proxy %s\n}\n", domain, gw))
	if baseDomain != "" && baseDomain != domain {
		sb.WriteString(fmt.Sprintf("\nhttp://*.%s {\n    reverse_proxy %s\n}\n", baseDomain, gw))
		sb.WriteString(fmt.Sprintf("\nhttp://%s {\n    reverse_proxy %s\n}\n", baseDomain, gw))
	}

	// Self-hosted ntfy reverse-proxy (feature #72). Emitted only when
	// the orchestrator has called EnableNtfyProxy on this installer —
	// i.e. this node was selected to host ntfy. The hostname is its
	// own block so the cert lives separately from the namespace gateway
	// cert (different rotation cadence, different blast radius).
	if ci.withNtfy && ci.ntfyHostname != "" {
		sb.WriteString(fmt.Sprintf("\n%s {\n%s\n    reverse_proxy localhost:%d\n}\n",
			ci.ntfyHostname, tlsBlock, NtfyListenPort))
	}

	// HTTP catch-all fallback (handles remaining plain HTTP traffic)
	sb.WriteString(fmt.Sprintf("\n:80 {\n    reverse_proxy %s\n}\n", gw))

	return sb.String()
}
