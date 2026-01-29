package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	caddyVersion = "2.10.2"
	xcaddyRepo   = "github.com/caddyserver/xcaddy/cmd/xcaddy@latest"
)

// CaddyInstaller handles Caddy installation with custom DNS module
type CaddyInstaller struct {
	*BaseInstaller
	version    string
	oramaHome  string
	dnsModule  string // Path to the orama DNS module source
}

// NewCaddyInstaller creates a new Caddy installer
func NewCaddyInstaller(arch string, logWriter io.Writer, oramaHome string) *CaddyInstaller {
	return &CaddyInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       caddyVersion,
		oramaHome:     oramaHome,
		dnsModule:     filepath.Join(oramaHome, "src", "pkg", "caddy", "dns", "orama"),
	}
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
		cmd.Env = append(os.Environ(), "PATH="+goPath, "GOBIN=/usr/local/bin")
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
	tidyCmd.Env = append(os.Environ(), "PATH="+goPath)
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
	buildCmd.Env = append(os.Environ(), "PATH="+goPath)
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

	// Grant CAP_NET_BIND_SERVICE to allow binding to ports 80/443
	if err := exec.Command("setcap", "cap_net_bind_service=+ep", dstBinary).Run(); err != nil {
		fmt.Fprintf(ci.logWriter, "    ⚠️  Warning: failed to setcap on caddy: %v\n", err)
	}

	fmt.Fprintf(ci.logWriter, "  ✓ Caddy with orama DNS module installed\n")
	return nil
}

// Configure creates Caddy configuration files
func (ci *CaddyInstaller) Configure(domain string, email string, acmeEndpoint string) error {
	configDir := "/etc/caddy"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create Caddyfile
	caddyfile := ci.generateCaddyfile(domain, email, acmeEndpoint)
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
	// Default: http://localhost:6001/v1/internal/acme
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
		p.Endpoint = "http://localhost:6001/v1/internal/acme"
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
	github.com/caddyserver/caddy/v2 v2.` + caddyVersion[2:] + `
	github.com/libdns/libdns v1.1.0
)
`
}

// generateCaddyfile creates the Caddyfile configuration
func (ci *CaddyInstaller) generateCaddyfile(domain, email, acmeEndpoint string) string {
	return fmt.Sprintf(`{
    email %s
}

*.%s {
    tls {
        dns orama {
            endpoint %s
        }
    }
    reverse_proxy localhost:6001
}

%s {
    tls {
        dns orama {
            endpoint %s
        }
    }
    reverse_proxy localhost:6001
}

:80 {
    reverse_proxy localhost:6001
}
`, email, domain, acmeEndpoint, domain, acmeEndpoint)
}
