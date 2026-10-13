package cloudflare

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// clusterLabelPattern is the label of a cluster subdomain e2e-<id>-<label>:
// a second, single-node cluster the run installs (provision.AddEvalCluster),
// or a name the run owns outside its delegated subdomain. No '-', so the run
// id and the label split unambiguously.
var clusterLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)

// maxTXTLength bounds one TXT value this client writes (a DNS character
// string is 255 bytes; the verification tokens are far shorter).
const maxTXTLength = 255

// ClusterSubdomain is e2e-<id>-<label>.<zone>: a subdomain the run owns
// beside its own. Cloudflare serves it (unless it is delegated), which is
// what a record the product must resolve over public DNS needs: a record
// under the run subdomain itself is hidden behind the delegation to the
// fleet's nameservers.
func (c *Client) ClusterSubdomain(runID, label string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", fmt.Errorf("run id %q must be 4-16 lowercase letters or digits", runID)
	}
	if !clusterLabelPattern.MatchString(label) {
		return "", fmt.Errorf("cluster label %q must be a lowercase letter then up to 11 letters or digits", label)
	}
	return RunPrefix + runID + "-" + label + "." + c.zone, nil
}

// managedSubdomain reports whether sub is a run subdomain or a cluster
// subdomain: exactly one run-owned label directly under the zone.
func (c *Client) managedSubdomain(sub string) bool {
	if _, ok := c.RunIDOf(sub); !ok {
		return false
	}
	label, zone, ok := strings.Cut(sub, ".")
	return ok && zone == c.zone && strings.HasPrefix(label, RunPrefix)
}

// SetTXT creates the TXT record name = value, leaving any other value of
// name in place (a name may hold several). A record that already holds value
// is left alone. name must be inside a run's subdomain or cluster subdomain.
func (c *Client) SetTXT(ctx context.Context, name, value string) error {
	name, err := c.checkTXT(name, value)
	if err != nil {
		return err
	}
	existing, err := c.ListTXT(ctx, name)
	if err != nil {
		return err
	}
	for _, r := range existing {
		if TXTValue(r.Content) == value {
			return nil
		}
	}
	zoneID, err := c.ZoneID(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{"type": "TXT", "name": name, "content": value, "ttl": autoTTL}
	if _, err := c.do(ctx, "POST", "/zones/"+zoneID+"/dns_records", body, nil); err != nil {
		return fmt.Errorf("failed to create TXT %s: %w", name, err)
	}
	return nil
}

// DeleteTXT deletes the TXT records of name holding value (every TXT record
// of name when value is empty) and returns what it deleted.
func (c *Client) DeleteTXT(ctx context.Context, name, value string) ([]Record, error) {
	name, err := c.checkName(name)
	if err != nil {
		return nil, err
	}
	existing, err := c.ListTXT(ctx, name)
	if err != nil {
		return nil, err
	}
	var doomed []Record
	for _, r := range existing {
		if value == "" || TXTValue(r.Content) == value {
			doomed = append(doomed, r)
		}
	}
	return c.deleteRecords(ctx, doomed)
}

// ListTXT returns the TXT records of name.
func (c *Client) ListTXT(ctx context.Context, name string) ([]Record, error) {
	name, err := c.checkName(name)
	if err != nil {
		return nil, err
	}
	recs, err := c.listRecords(ctx, url.Values{"type": {"TXT"}, "name": {name}})
	if err != nil {
		return nil, fmt.Errorf("failed to read TXT %s: %w", name, err)
	}
	return recs, nil
}

// TXTValue is a TXT record's content without the quotes Cloudflare may add.
func TXTValue(content string) string {
	if len(content) >= 2 && strings.HasPrefix(content, `"`) && strings.HasSuffix(content, `"`) {
		return content[1 : len(content)-1]
	}
	return content
}

func (c *Client) checkTXT(name, value string) (string, error) {
	if value == "" || len(value) > maxTXTLength || strings.ContainsAny(value, "\"\r\n\x00") {
		return "", fmt.Errorf("refusing TXT value for %s: it must be 1-%d bytes with no quote, newline or NUL", name, maxTXTLength)
	}
	return c.checkName(name)
}

// checkName normalises name and refuses one outside every run.
func (c *Client) checkName(name string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if _, ok := c.RunIDOf(n); !ok {
		return "", fmt.Errorf("refusing %q: only names inside a run subdomain %s<id>[-<cluster>].%s are managed", name, RunPrefix, c.zone)
	}
	return n, nil
}
