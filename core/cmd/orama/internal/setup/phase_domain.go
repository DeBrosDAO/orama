package setup

import (
	"context"
	"fmt"
)

// domainBeforeJoin holds the first join of a run on a cluster of its own domain
// until the domain is delegated and the cluster serves a certificate for it. An
// invite pins the certificate the minting node serves for the domain, and on a
// domain nobody has delegated yet the cluster has none: it is issued over DNS-01
// by the cluster's own nameservers. So the records of the nameservers the cluster
// has so far (the first machine's, on a new cluster) are printed and waited for
// before any machine joins; the ones the joined machines add are printed at the end.
// On a cluster that is already delegated the wait returns at the first look.
func (r *runner) domainBeforeJoin(ctx context.Context, n *nodeRun) error {
	if r.domainReady || n.plan.Cluster == ClusterCreate || n.facts.ClusterInstalled ||
		r.opts.Domain == "" || r.d.Domain == nil || r.via == nil {
		return nil
	}
	r.announceDomain(ctx)
	r.emit("", StepDNS, StateRunning, "waiting for "+r.opts.Domain+" to be delegated before "+n.plan.IP+" joins")
	err := r.d.Domain.Wait(ctx, r.via, r.opts.Domain, r.d.Timing.DNSPoll, r.d.Timing.DNSDeadline)
	if err != nil {
		r.emit("", StepDNS, StateFailed, err.Error())
		// The records printed before the wait may not have been readable yet;
		// print them again so the error's "the records above" names them.
		r.announceDomain(ctx)
		return fmt.Errorf("%s is not delegated yet: %w\n  %s cannot join before it is: an invite pins the certificate the cluster serves for %s, which it is issued once the domain is delegated to it; create the records above, then run `%s`, which resumes at this step",
			r.opts.Domain, err, n.plan.IP, r.opts.Domain, r.resumeCommand())
	}
	r.domainReady = true
	r.emit("", StepDNS, StateDone, r.opts.Domain)
	return nil
}

// announceDomain prints the NS and glue records a private cluster's domain needs
// from the nameservers the cluster has: before the first join (domainBeforeJoin),
// and again once every machine is in, for the nameservers the joins added. DNS is
// waited for at the end (waitDomain).
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
