package orama

// dns.providers.orama answers DNS-01 challenges through the index gateway's
// internal ACME API, which publishes the TXT record in the cluster's own DNS.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	// Endpoint is the URL of the Orama gateway's ACME API, the index
	// gateway's /v1/internal/acme. Required.
	Endpoint string `json:"endpoint,omitempty"`

	// KeyFile holds the hex key every call is signed with. Required: the
	// gateway refuses an unsigned call.
	KeyFile string `json:"key_file,omitempty"`

	key []byte
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
		return fmt.Errorf("orama DNS provider: endpoint is required; install writes the gateway's /v1/internal/acme into the Caddyfile")
	}
	if p.KeyFile == "" {
		return fmt.Errorf("orama DNS provider: key_file is required; the gateway refuses unsigned DNS-01 calls")
	}
	key, err := readHexKey(p.KeyFile, "orama DNS provider")
	if err != nil {
		return err
	}
	p.key = key
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
			case "key_file":
				if !d.NextArg() {
					return d.ArgErr()
				}
				p.KeyFile = d.Val()
			default:
				return d.Errf("unrecognized option: %s", d.Val())
			}
		}
	}
	return nil
}

// sign stamps req the way the gateway's auth.VerifyACME checks it: the MAC
// covers the body, so a captured stamp cannot be replayed with another record.
func sign(key []byte, req *http.Request, body []byte, now time.Time) {
	ts := strconv.FormatInt(now.Unix(), 10)
	sum := sha256.Sum256(body)
	payload := strings.Join([]string{"orama-coordination-v2", strings.ToUpper(req.Method), req.URL.Path, req.URL.RawQuery, hex.EncodeToString(sum[:]), ts}, "\n")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	req.Header.Set(acmeMACHeader, ts+"."+hex.EncodeToString(mac.Sum(nil)))
}

// call posts one record to the gateway's present or cleanup endpoint.
func (p *Provider) call(ctx context.Context, op string, zone string, rr libdns.RR) error {
	body, err := json.Marshal(map[string]string{"fqdn": rr.Name + "." + zone, "value": rr.Data})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	u, err := url.Parse(p.Endpoint + "/" + op)
	if err != nil {
		return fmt.Errorf("orama DNS provider: endpoint %q: %w", p.Endpoint, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	sign(p.key, req, body, time.Now())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to %s challenge: %w", op, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s failed with status %d", op, resp.StatusCode)
	}
	return nil
}

// AppendRecords adds records to the zone.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var added []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}
		if err := p.call(ctx, "present", zone, rr); err != nil {
			return added, err
		}
		added = append(added, rec)
	}
	return added, nil
}

// DeleteRecords removes records from the zone.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var deleted []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}
		if err := p.call(ctx, "cleanup", zone, rr); err != nil {
			return deleted, err
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
