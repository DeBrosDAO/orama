package dnsdelegation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare writes NS and glue records into a parent zone, then checks that
// a resolver returns them. The token is a Cloudflare API token with DNS edit
// on that zone. Records are created with proxied=false: glue must not be
// orange-clouded.
type Cloudflare struct {
	Token string
	// Base is the API origin, set by tests. Empty uses the public API.
	Base string
	HTTP *http.Client
	// LookupNS and LookupHost default to the system resolver.
	LookupNS   func(ctx context.Context, name string) ([]string, error)
	LookupHost func(ctx context.Context, name string) ([]string, error)
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Apply creates or updates the NS and glue records for d, then checks DNS.
func (c *Cloudflare) Apply(ctx context.Context, d Delegation) error {
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("cloudflare token is empty")
	}
	parent := ParentZone(d.Domain)
	if parent == "" || !strings.Contains(parent, ".") {
		return fmt.Errorf("zone %q is a registry TLD; create the nameservers at the registrar, not through the Cloudflare API", d.Domain)
	}
	zoneID, err := c.zoneID(ctx, parent)
	if err != nil {
		return err
	}
	for _, ns := range d.Nameservers {
		fqdn := ns.Hostname + "." + d.Domain
		if err := c.upsert(ctx, zoneID, "NS", d.Domain, fqdn); err != nil {
			return err
		}
		if err := c.upsert(ctx, zoneID, "A", fqdn, ns.IP); err != nil {
			return err
		}
	}
	return c.verify(ctx, d)
}

func (c *Cloudflare) verify(ctx context.Context, d Delegation) error {
	lookupNS := c.LookupNS
	if lookupNS == nil {
		lookupNS = func(ctx context.Context, name string) ([]string, error) {
			hosts, err := net.DefaultResolver.LookupNS(ctx, name)
			if err != nil {
				return nil, err
			}
			out := make([]string, len(hosts))
			for i, h := range hosts {
				out[i] = h.Host
			}
			return out, nil
		}
	}
	lookupHost := c.LookupHost
	if lookupHost == nil {
		lookupHost = func(ctx context.Context, name string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, name)
		}
	}
	gotNS, err := lookupNS(ctx, d.Domain)
	if err != nil {
		return fmt.Errorf("look up NS for %s: %w", d.Domain, err)
	}
	for _, ns := range d.Nameservers {
		fqdn := ns.Hostname + "." + d.Domain
		if !containsName(gotNS, fqdn) {
			return fmt.Errorf("NS for %s does not include %s (got %s)", d.Domain, fqdn, strings.Join(gotNS, ", "))
		}
		hosts, err := lookupHost(ctx, fqdn)
		if err != nil {
			return fmt.Errorf("look up glue %s: %w", fqdn, err)
		}
		if !containsName(hosts, ns.IP) {
			return fmt.Errorf("glue %s is %s, want %s", fqdn, strings.Join(hosts, ", "), ns.IP)
		}
	}
	return nil
}

func containsName(have []string, want string) bool {
	want = strings.TrimSuffix(strings.ToLower(want), ".")
	for _, h := range have {
		if strings.TrimSuffix(strings.ToLower(h), ".") == want {
			return true
		}
	}
	return false
}

func (c *Cloudflare) zoneID(ctx context.Context, name string) (string, error) {
	q := url.Values{"name": {name}}
	var resp struct {
		Success bool `json:"success"`
		Result  []struct {
			ID string `json:"id"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.get(ctx, "/zones?"+q.Encode(), &resp); err != nil {
		return "", err
	}
	if !resp.Success || len(resp.Result) == 0 {
		return "", fmt.Errorf("cloudflare has no zone named %s", name)
	}
	return resp.Result[0].ID, nil
}

func (c *Cloudflare) upsert(ctx context.Context, zoneID, typ, name, content string) error {
	existing, err := c.find(ctx, zoneID, typ, name, content)
	if err != nil {
		return err
	}
	body := map[string]any{"type": typ, "name": name, "content": content, "ttl": 1, "proxied": false}
	if typ == "NS" {
		delete(body, "proxied")
	}
	if existing == nil {
		return c.post(ctx, "/zones/"+zoneID+"/dns_records", body)
	}
	if strings.EqualFold(existing.Content, content) {
		return nil
	}
	return c.put(ctx, "/zones/"+zoneID+"/dns_records/"+existing.ID, body)
}

func (c *Cloudflare) find(ctx context.Context, zoneID, typ, name, content string) (*cfRecord, error) {
	q := url.Values{"type": {typ}, "name": {name}}
	var resp struct {
		Success bool       `json:"success"`
		Result  []cfRecord `json:"result"`
	}
	if err := c.get(ctx, "/zones/"+zoneID+"/dns_records?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	if typ == "NS" {
		for i := range resp.Result {
			if strings.EqualFold(strings.TrimSuffix(resp.Result[i].Content, "."), strings.TrimSuffix(content, ".")) {
				return &resp.Result[i], nil
			}
		}
		return nil, nil
	}
	if len(resp.Result) == 0 {
		return nil, nil
	}
	return &resp.Result[0], nil
}

func (c *Cloudflare) get(ctx context.Context, path string, dest any) error {
	return c.do(ctx, http.MethodGet, path, nil, dest)
}

func (c *Cloudflare) post(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPost, path, body, nil)
}

func (c *Cloudflare) put(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPut, path, body, nil)
}

func (c *Cloudflare) do(ctx context.Context, method, path string, body, dest any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	base := c.Base
	if base == "" {
		base = cloudflareAPI
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare %s %s: HTTP %d: %s", method, path, resp.StatusCode, raw)
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	return nil
}
