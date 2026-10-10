package setup

import (
	"context"
	"fmt"
	"strings"
)

// announceDomain prints the NS and glue records a private cluster's domain needs
// as soon as the cluster exists, so the operator can create them while the rest
// installs. DNS is waited for at the end (waitDomain).
func (r *runner) announceDomain(ctx context.Context) {
	if r.opts.Domain == "" || r.d.Domain == nil {
		return
	}
	records, err := r.d.Domain.Records(ctx, r.plan.Env, r.opts.Domain)
	if err != nil {
		r.d.Report.Linef("  could not read the records %s needs from the cluster yet: %v", r.opts.Domain, err)
		return
	}
	r.d.Report.Linef("  Create these records in the parent zone of %s (at your registrar or DNS host):", r.opts.Domain)
	for _, rec := range records {
		r.d.Report.Linef("    %s", rec)
	}
}

// waitDomain waits until the parent zone returns the cluster's records and the
// cluster serves a certificate for its domain. If the deadline passes the
// install is complete and only DNS is not; the error says how to resume.
func (r *runner) waitDomain(ctx context.Context) error {
	if r.opts.Domain == "" || r.d.Domain == nil {
		return nil
	}
	r.emit("", StepDNS, StateRunning, "waiting for "+r.opts.Domain+" to be delegated")
	err := r.d.Domain.Wait(ctx, r.plan.Env, r.opts.Domain, r.d.Timing.DNSPoll, r.d.Timing.DNSDeadline)
	if err != nil {
		r.emit("", StepDNS, StateFailed, err.Error())
		r.res.Pending = append(r.res.Pending, "delegate "+r.opts.Domain+" (the records above), then run: "+r.resumeCommand())
		return fmt.Errorf("%s is not delegated yet: %w\n  everything else is installed; create the records above, then run `%s`, which resumes at this step",
			r.opts.Domain, err, r.resumeCommand())
	}
	r.emit("", StepDNS, StateDone, r.opts.Domain)
	return nil
}

// resumeCommand is the command that runs the same setup again.
func (r *runner) resumeCommand() string {
	parts := []string{"orama setup", "--network " + r.plan.Network, "--domain " + r.opts.Domain, "--env " + r.plan.Env}
	for _, n := range r.plan.Nodes {
		parts = append(parts, "--ip "+n.IP)
	}
	if r.opts.Name != "" {
		parts = append(parts, "--name "+r.opts.Name)
	}
	if r.opts.ClusterOnly {
		parts = append(parts, "--cluster-only")
	}
	return strings.Join(parts, " ")
}
