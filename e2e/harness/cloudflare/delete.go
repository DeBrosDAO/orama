package cloudflare

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ListUnder returns every record named subdomain or a name under it.
// subdomain must be a run subdomain.
func (c *Client) ListUnder(ctx context.Context, subdomain string) ([]Record, error) {
	if !c.managedSubdomain(subdomain) {
		return nil, fmt.Errorf("refusing to list %q: only a run subdomain %s<id>[-<cluster>].%s is managed", subdomain, RunPrefix, c.zone)
	}
	all, err := c.listRecords(ctx, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("failed to list the records of %s: %w", c.zone, err)
	}
	var under []Record
	for _, r := range all {
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		if name == subdomain || strings.HasSuffix(name, "."+subdomain) {
			under = append(under, r)
		}
	}
	return under, nil
}

// DeleteUnder deletes every record named subdomain or under it, then lists
// again and fails if any is left. It returns what it deleted; a subdomain
// with no records deletes nothing and is not an error.
func (c *Client) DeleteUnder(ctx context.Context, subdomain string) ([]Record, error) {
	records, err := c.ListUnder(ctx, subdomain)
	if err != nil {
		return nil, err
	}
	deleted, err := c.deleteRecords(ctx, records)
	if err != nil {
		return deleted, err
	}
	left, err := c.ListUnder(ctx, subdomain)
	if err != nil {
		return deleted, fmt.Errorf("failed to verify the records of %s are gone: %w", subdomain, err)
	}
	if len(left) > 0 {
		return deleted, fmt.Errorf("%d records under %s are still there after deleting them (first: %s %s)",
			len(left), subdomain, left[0].Type, left[0].Name)
	}
	return deleted, nil
}

// RunRecords returns every record inside any run subdomain of the zone, for
// the orphan sweep.
func (c *Client) RunRecords(ctx context.Context) ([]Record, error) {
	all, err := c.listRecords(ctx, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("failed to list the records of %s: %w", c.zone, err)
	}
	var runs []Record
	for _, r := range all {
		if _, ok := c.RunIDOf(r.Name); ok {
			runs = append(runs, r)
		}
	}
	return runs, nil
}

// DeleteRecords deletes the given records, which must all be inside run
// subdomains. A record already gone counts as deleted.
func (c *Client) DeleteRecords(ctx context.Context, records []Record) ([]Record, error) {
	for _, r := range records {
		if _, ok := c.RunIDOf(r.Name); !ok {
			return nil, fmt.Errorf("refusing to delete %s %s: it is not inside a run subdomain", r.Type, r.Name)
		}
	}
	return c.deleteRecords(ctx, records)
}

func (c *Client) deleteRecords(ctx context.Context, records []Record) ([]Record, error) {
	zoneID, err := c.ZoneID(ctx)
	if err != nil {
		return nil, err
	}
	var deleted []Record
	for _, r := range records {
		_, err := c.do(ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+r.ID, nil, nil)
		if err != nil && !IsNotFound(err) {
			return deleted, fmt.Errorf("failed to delete %s %s: %w", r.Type, r.Name, err)
		}
		deleted = append(deleted, r)
	}
	return deleted, nil
}
