package provision

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

// Sweep deletes what crashed runs left: every Hetzner server, firewall and
// SSH key named e2e-... and labelled e2e-run with a run id that is past its
// own e2e-ttl (or, without one, older than maxAge), and every Cloudflare
// record inside an e2e-<id> subdomain whose run has no live server. A
// firewall, SSH key or record of a run with a live server, and a record
// younger than dnsGrace, are kept. It returns what it removed.
func Sweep(ctx context.Context, maxAge time.Duration, log Logger) ([]string, error) {
	d, err := depsFromEnv()
	if err != nil {
		return nil, err
	}
	return sweep(ctx, maxAge, log, d, time.Now())
}

// dnsGrace is the youngest a run record must be before the sweep judges it:
// a run that is starting may have written records whose server the sweep's
// listings did not see yet.
const dnsGrace = 30 * time.Minute

func sweep(ctx context.Context, maxAge time.Duration, log Logger, d deps, now time.Time) ([]string, error) {
	if maxAge <= 0 {
		return nil, fmt.Errorf("sweep max age must be positive, got %s", maxAge)
	}
	var removed []string
	live, gone, err := sweepServers(ctx, maxAge, d, now)
	removed = append(removed, gone...)
	errs := []error{err}
	gone, err = sweepAccess(ctx, maxAge, live, d, now)
	removed, errs = append(removed, gone...), append(errs, err)
	gone, err = sweepRecords(ctx, maxAge, d, now)
	removed, errs = append(removed, gone...), append(errs, err)
	for _, r := range removed {
		log.Infof("swept %s", r)
	}
	return removed, errors.Join(errs...)
}

// sweepServers deletes expired run servers and returns the runs that still
// have a live one; nil when the servers could not be listed.
func sweepServers(ctx context.Context, maxAge time.Duration, d deps, now time.Time) (map[string]bool, []string, error) {
	servers, err := d.cloud.ListServers(ctx, hetzner.LabelRun)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list e2e servers: %w", err)
	}
	live, doomed := splitLive(servers, maxAge, now)
	var removed []string
	var errs []error
	for _, s := range doomed {
		if err := d.cloud.DeleteServer(ctx, s.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := waitServerGone(ctx, d, s); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, fmt.Sprintf("server %s (%d, run %s)", s.Name, s.ID, s.Labels[hetzner.LabelRun]))
	}
	return live, removed, errors.Join(errs...)
}

// splitLive separates expired servers from those whose run is live.
func splitLive(servers []hetzner.Server, maxAge time.Duration, now time.Time) (map[string]bool, []hetzner.Server) {
	live := map[string]bool{}
	var doomed []hetzner.Server
	for _, s := range servers {
		if !sweepable(s.Name, s.Labels) {
			continue
		}
		if expired(s.Labels, s.Created, now, maxAge) {
			doomed = append(doomed, s)
		} else {
			live[s.Labels[hetzner.LabelRun]] = true
		}
	}
	return live, doomed
}

// sweepAccess deletes expired run firewalls and SSH keys, except those of a
// run with a live server: its servers still use them.
func sweepAccess(ctx context.Context, maxAge time.Duration, live map[string]bool, d deps, now time.Time) ([]string, error) {
	if live == nil {
		return nil, errors.New("not sweeping firewalls and SSH keys: the live runs are unknown because listing servers failed")
	}
	doomed := func(name string, labels map[string]string, created time.Time) bool {
		return sweepable(name, labels) && !live[labels[hetzner.LabelRun]] && expired(labels, created, now, maxAge)
	}
	var removed []string
	var errs []error
	fws, err := d.cloud.ListFirewalls(ctx, hetzner.LabelRun)
	errs = append(errs, err)
	for _, fw := range fws {
		if !doomed(fw.Name, fw.Labels, fw.Created) {
			continue
		}
		if err := d.cloud.DeleteFirewall(ctx, fw.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, fmt.Sprintf("firewall %s (%d)", fw.Name, fw.ID))
	}
	keys, err := d.cloud.ListSSHKeys(ctx, hetzner.LabelRun)
	errs = append(errs, err)
	for _, k := range keys {
		if !doomed(k.Name, k.Labels, k.Created) {
			continue
		}
		if err := d.cloud.DeleteSSHKey(ctx, k.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, fmt.Sprintf("SSH key %s (%d)", k.Name, k.ID))
	}
	return removed, errors.Join(errs...)
}

// sweepRecords deletes the records of runs that have no live server. A
// live run's records are kept whatever their age. The live runs are listed
// afresh after the records are, so a run that started while the sweep ran
// is seen; and a record created after the sweep started, or younger than
// dnsGrace, is not judged at all.
func sweepRecords(ctx context.Context, maxAge time.Duration, d deps, now time.Time) ([]string, error) {
	records, err := d.dns.RunRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list the run DNS records: %w", err)
	}
	servers, err := d.cloud.ListServers(ctx, hetzner.LabelRun)
	if err != nil {
		return nil, fmt.Errorf("not sweeping DNS: the live runs are unknown because listing servers failed: %w", err)
	}
	live, _ := splitLive(servers, maxAge, now)
	var doomed []cloudflare.Record
	for _, rec := range records {
		age := now.Sub(rec.CreatedOn)
		if age < dnsGrace {
			continue
		}
		if id, _ := d.dns.RunIDOf(rec.Name); !live[id] {
			doomed = append(doomed, rec)
		}
	}
	deleted, err := d.dns.DeleteRecords(ctx, doomed)
	var removed []string
	for _, rec := range deleted {
		removed = append(removed, fmt.Sprintf("DNS %s %s -> %s", rec.Type, rec.Name, rec.Content))
	}
	return removed, err
}
