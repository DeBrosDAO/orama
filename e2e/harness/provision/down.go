package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

// Down removes everything the run owns: its servers (nodes, probes and
// extras, found by the e2e-run label, not by the state's lists), its firewall
// and SSH key, every DNS record under its subdomain, and the test agent with
// its directory (wallet, password, SSH key), shredded. It is idempotent: a
// second call finds nothing and succeeds. Each part is attempted even when
// another fails; the errors are joined.
func Down(ctx context.Context, st *fleet.State, log Logger) error {
	d, err := depsFromEnv()
	if err != nil {
		return err
	}
	return down(ctx, st, log, d)
}

func down(ctx context.Context, st *fleet.State, log Logger, d deps) error {
	if st == nil || !runIDPattern.MatchString(st.RunID) {
		return errors.New("refusing to tear down: the state has no valid run id")
	}
	errs := []error{
		deleteRunServers(ctx, st.RunID, log, d),
		deleteRunAccess(ctx, st.RunID, log, d),
		deleteRunRecords(ctx, st, log, d),
	}
	errs = append(errs, verifyNothingLeft(ctx, st.RunID, d))
	if st.Home != "" {
		if err := d.stopAgentDir(ctx, st.Home); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop the test agent in %s: %w", st.Home, err))
		} else {
			log.Infof("stopped the test agent and shredded %s", st.Home)
		}
	}
	return errors.Join(errs...)
}

// verifyNothingLeft lists the run's servers, firewalls and SSH keys by label
// once more: Down succeeds only when none is left.
func verifyNothingLeft(ctx context.Context, runID string, d deps) error {
	sel := runSelector(runID)
	servers, serr := d.cloud.ListServers(ctx, sel)
	fws, ferr := d.cloud.ListFirewalls(ctx, sel)
	keys, kerr := d.cloud.ListSSHKeys(ctx, sel)
	if err := errors.Join(serr, ferr, kerr); err != nil {
		return fmt.Errorf("failed to check that run %s left nothing: %w", runID, err)
	}
	if n := len(servers) + len(fws) + len(keys); n > 0 {
		return fmt.Errorf("teardown of run %s left %d servers, %d firewalls and %d SSH keys labelled %s",
			runID, len(servers), len(fws), len(keys), sel)
	}
	return nil
}

// deleteRunServers deletes every server labelled with the run, then waits
// until each is gone (a firewall cannot be deleted while applied).
func deleteRunServers(ctx context.Context, runID string, log Logger, d deps) error {
	servers, err := d.cloud.ListServers(ctx, runSelector(runID))
	if err != nil {
		return fmt.Errorf("failed to list the servers of run %s: %w", runID, err)
	}
	var errs []error
	for _, s := range servers {
		if err := d.cloud.DeleteServer(ctx, s.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		log.Infof("deleting server %s (%d)", s.Name, s.ID)
	}
	for _, s := range servers {
		errs = append(errs, waitServerGone(ctx, d, s))
	}
	return errors.Join(errs...)
}

func waitServerGone(ctx context.Context, d deps, s hetzner.Server) error {
	wctx, cancel := context.WithTimeout(ctx, d.timing.serverGone)
	defer cancel()
	if err := d.cloud.WaitGone(wctx, s.ID, d.timing.poll); err != nil {
		return fmt.Errorf("server %s (%d) is still there: %w", s.Name, s.ID, err)
	}
	return nil
}

// deleteRunAccess deletes the run's firewalls and SSH keys.
func deleteRunAccess(ctx context.Context, runID string, log Logger, d deps) error {
	var errs []error
	fws, err := d.cloud.ListFirewalls(ctx, runSelector(runID))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list the firewalls of run %s: %w", runID, err))
	}
	for _, fw := range fws {
		if err := d.cloud.DeleteFirewall(ctx, fw.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		log.Infof("deleted firewall %s", fw.Name)
	}
	keys, err := d.cloud.ListSSHKeys(ctx, runSelector(runID))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list the SSH keys of run %s: %w", runID, err))
	}
	for _, k := range keys {
		if err := d.cloud.DeleteSSHKey(ctx, k.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		log.Infof("deleted SSH key %s", k.Name)
	}
	return errors.Join(errs...)
}

// deleteRunRecords deletes every record under the run's subdomain.
func deleteRunRecords(ctx context.Context, st *fleet.State, log Logger, d deps) error {
	sub, err := d.dns.RunSubdomain(st.RunID)
	if err != nil {
		return err
	}
	if st.BaseDomain != "" && st.BaseDomain != sub {
		return fmt.Errorf("the state's base domain %s is not the run subdomain %s; refusing to touch it", st.BaseDomain, sub)
	}
	deleted, err := d.dns.DeleteUnder(ctx, sub)
	if err != nil {
		return fmt.Errorf("failed to delete the DNS records of %s: %w", sub, err)
	}
	log.Infof("deleted %d DNS records under %s", len(deleted), sub)
	return deleteClusterRecords(ctx, st.RunID, log, d)
}

// deleteClusterRecords deletes the records of the run's cluster subdomains
// (e2e-<id>-<label>: eval clusters, names a feature wrote through the
// broker), which live beside the run subdomain, not under it.
func deleteClusterRecords(ctx context.Context, runID string, log Logger, d deps) error {
	all, err := d.dns.RunRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to list the cluster subdomain records of run %s: %w", runID, err)
	}
	var own []cloudflare.Record
	for _, r := range all {
		if id, ok := d.dns.RunIDOf(r.Name); ok && id == runID {
			own = append(own, r)
		}
	}
	deleted, err := d.dns.DeleteRecords(ctx, own)
	if err != nil {
		return fmt.Errorf("failed to delete the cluster subdomain records of run %s: %w", runID, err)
	}
	if len(deleted) > 0 {
		log.Infof("deleted %d DNS records of the cluster subdomains of run %s", len(deleted), runID)
	}
	return nil
}

// expired reports whether a resource is past its own e2e-ttl or, when it
// carries none that parses, past maxAge. A run's e2e-ttl covers its whole
// stage plan (cmd/e2e-fleet derives it), so a max age shorter than a live
// run can never take it.
func expired(labels map[string]string, created, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(created)
	if ttl, err := time.ParseDuration(labels[hetzner.LabelTTL]); err == nil && ttl > 0 {
		return age > ttl
	}
	return age > maxAge
}

// sweepable reports whether a labelled resource is an e2e run's: its e2e-run
// label has the run id's shape and its name starts with e2e-. Anything else
// carrying the label (a hand-made server, another tool's) is never touched.
func sweepable(name string, labels map[string]string) bool {
	return runIDPattern.MatchString(labels[hetzner.LabelRun]) && strings.HasPrefix(name, namePrefix)
}
