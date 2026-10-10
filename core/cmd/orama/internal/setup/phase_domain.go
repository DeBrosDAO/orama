package setup

import (
	"context"
	"fmt"
)

// announceDomain prints the NS and glue records a private cluster's domain needs
// as soon as the cluster exists, so the operator can create them while the rest
// installs. DNS is waited for at the end (waitDomain).
func (r *runner) announceDomain(ctx context.Context) {
	if r.opts.Domain == "" || r.d.Domain == nil || r.via == nil {
		return
	}
	records, err := r.d.Domain.Records(ctx, r.via, r.opts.Domain)
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
	if r.opts.Domain == "" || r.d.Domain == nil || r.via == nil {
		return nil
	}
	r.emit("", StepDNS, StateRunning, "waiting for "+r.opts.Domain+" to be delegated")
	err := r.d.Domain.Wait(ctx, r.via, r.opts.Domain, r.d.Timing.DNSPoll, r.d.Timing.DNSDeadline)
	if err != nil {
		r.emit("", StepDNS, StateFailed, err.Error())
		return fmt.Errorf("%s is not delegated yet: %w\n  everything else is installed; create the records above, then run `%s`, which resumes at this step",
			r.opts.Domain, err, r.resumeCommand())
	}
	r.emit("", StepDNS, StateDone, r.opts.Domain)
	return nil
}

// resumeCommand is the command that runs the same setup again.
func (r *runner) resumeCommand() string {
	o := r.opts
	o.Env = r.plan.Env
	if o.Create == nil {
		o.Network = r.plan.Network
	}
	return o.CommandLine()
}
