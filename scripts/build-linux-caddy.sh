#!/bin/bash
# Build Caddy with orama DNS module for linux/amd64
# Outputs to bin-linux/caddy
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="$PROJECT_ROOT/bin-linux"
BUILD_DIR="/tmp/caddy-build-linux"
MODULE_DIR="$BUILD_DIR/caddy-dns-orama"

mkdir -p "$OUTPUT_DIR"

# Ensure xcaddy is installed
if ! command -v xcaddy &> /dev/null; then
    echo "Installing xcaddy..."
    go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
fi

# Clean up previous build
rm -rf "$BUILD_DIR"
mkdir -p "$MODULE_DIR"

# Write go.mod
cat > "$MODULE_DIR/go.mod" << 'GOMOD'
module github.com/DeBrosOfficial/caddy-dns-orama

go 1.22

require (
	github.com/caddyserver/caddy/v2 v2.10.2
	github.com/libdns/libdns v1.1.0
)
GOMOD

# Write provider.go (the orama DNS provider for ACME DNS-01 challenges)
cat > "$MODULE_DIR/provider.go" << 'PROVIDERGO'
// Package orama implements a DNS provider for Caddy that uses the Orama Network
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
	Endpoint string `json:"endpoint,omitempty"`
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
PROVIDERGO

# Run go mod tidy
cd "$MODULE_DIR" && go mod tidy

# Build with xcaddy
echo "Building Caddy binary..."
GOOS=linux GOARCH=amd64 xcaddy build v2.10.2 \
    --with "github.com/DeBrosOfficial/caddy-dns-orama=$MODULE_DIR" \
    --output "$OUTPUT_DIR/caddy"

# Cleanup
rm -rf "$BUILD_DIR"
echo "✓ Caddy built: bin-linux/caddy"
