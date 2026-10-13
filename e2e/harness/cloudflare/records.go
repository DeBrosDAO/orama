package cloudflare

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	// RunPrefix starts every run subdomain's first label.
	RunPrefix = "e2e-"
	// autoTTL is Cloudflare's "automatic" TTL.
	autoTTL = 1
)

// runIDPattern is a run id: it becomes a DNS label and a Hetzner label value.
var runIDPattern = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

// slotPattern is a nameserver slot the cluster claims (ns1, ns2, ...).
var slotPattern = regexp.MustCompile(`^ns[1-9][0-9]*$`)

// Nameserver is one delegated nameserver: slot nsN served at IP.
type Nameserver struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

// RunSubdomain is the subdomain of run id in the zone, e2e-<id>.<zone>.
func (c *Client) RunSubdomain(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", fmt.Errorf("run id %q must be 4-16 lowercase letters or digits", runID)
	}
	return RunPrefix + runID + "." + c.zone, nil
}

// RunIDOf returns the run id whose subdomain holds name, if any.
func (c *Client) RunIDOf(name string) (string, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	rest, ok := strings.CutSuffix(name, "."+c.zone)
	if !ok {
		return "", false
	}
	labels := strings.Split(rest, ".")
	last := labels[len(labels)-1]
	owned, ok := strings.CutPrefix(last, RunPrefix)
	if !ok {
		return "", false
	}
	// e2e-<id> is the run subdomain; e2e-<id>-<label> one of its cluster
	// subdomains (ClusterSubdomain). A run id holds no '-'.
	id, label, hasLabel := strings.Cut(owned, "-")
	if !runIDPattern.MatchString(id) || (hasLabel && !clusterLabelPattern.MatchString(label)) {
		return "", false
	}
	return id, true
}

// Delegate points subdomain at its nameservers: an NS record per slot and the
// glue A record giving each slot its address. Existing records that already
// say the same are left alone.
func (c *Client) Delegate(ctx context.Context, subdomain string, nss []Nameserver) error {
	if !c.managedSubdomain(subdomain) {
		return fmt.Errorf("refusing to delegate %q: only a run subdomain %s<id>[-<cluster>].%s is delegated", subdomain, RunPrefix, c.zone)
	}
	if len(nss) == 0 {
		return fmt.Errorf("refusing to delegate %s to no nameservers", subdomain)
	}
	for _, ns := range nss {
		ip := net.ParseIP(ns.IP)
		if !slotPattern.MatchString(ns.Hostname) || ip == nil || ip.To4() == nil {
			return fmt.Errorf("refusing nameserver %q at %q: want nsN and an IPv4 address", ns.Hostname, ns.IP)
		}
	}
	for _, ns := range nss {
		fqdn := ns.Hostname + "." + subdomain
		if err := c.upsert(ctx, "NS", subdomain, fqdn); err != nil {
			return err
		}
		if err := c.upsert(ctx, "A", fqdn, ns.IP); err != nil {
			return err
		}
	}
	return nil
}

// upsert creates the record, or updates the one of that type and name (for
// NS: that type, name and content) when its content differs.
func (c *Client) upsert(ctx context.Context, typ, name, content string) error {
	zoneID, err := c.ZoneID(ctx)
	if err != nil {
		return err
	}
	existing, err := c.listRecords(ctx, url.Values{"type": {typ}, "name": {name}})
	if err != nil {
		return fmt.Errorf("failed to read %s %s: %w", typ, name, err)
	}
	body := map[string]any{"type": typ, "name": name, "content": content, "ttl": autoTTL}
	if typ != "NS" {
		body["proxied"] = false
	}
	for _, r := range existing {
		if strings.EqualFold(strings.TrimSuffix(r.Content, "."), content) {
			return nil
		}
		if typ != "NS" {
			if _, err := c.do(ctx, "PUT", "/zones/"+zoneID+"/dns_records/"+r.ID, body, nil); err != nil {
				return fmt.Errorf("failed to update %s %s: %w", typ, name, err)
			}
			return nil
		}
	}
	if _, err := c.do(ctx, "POST", "/zones/"+zoneID+"/dns_records", body, nil); err != nil {
		return fmt.Errorf("failed to create %s %s -> %s: %w", typ, name, content, err)
	}
	return nil
}

// listRecords returns every record of the zone matching filter, all pages.
func (c *Client) listRecords(ctx context.Context, filter url.Values) ([]Record, error) {
	zoneID, err := c.ZoneID(ctx)
	if err != nil {
		return nil, err
	}
	var all []Record
	for page := 1; ; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
		for k, v := range filter {
			q[k] = v
		}
		var batch []Record
		env, err := c.do(ctx, "GET", "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if env.ResultInfo.TotalPages <= page {
			return all, nil
		}
	}
}
