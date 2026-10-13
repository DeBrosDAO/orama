package setup

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
		if errors.As(err, new(*issuanceRefused)) {
			return fmt.Errorf("%s has no certificate: %w\n  %s cannot join before it has one: an invite pins the certificate the cluster serves for %s; run `%s` once it can be issued, which resumes at this step",
				r.opts.Domain, err, n.plan.IP, r.opts.Domain, r.resumeCommand())
		}
		return fmt.Errorf("%s is not delegated yet: %w\n  %s cannot join before it is: an invite pins the certificate the cluster serves for %s, which it is issued once the domain is delegated to it; create the records above, then run `%s`, which resumes at this step",
			r.opts.Domain, err, n.plan.IP, r.opts.Domain, r.resumeCommand())
	}
	r.domainReady = true
	r.emit("", StepDNS, StateDone, r.opts.Domain)
	return nil
}

// nameserverSlots is how many nameserver slots a cluster has
// (pkg/node/dns_nameservers.go maxNameserverSlots): a cluster of more machines
// has that many nameservers and no more.
const nameserverSlots = 13

// announceAllNameservers prints the records once every machine of the run has its
// nameserver slot. A node claims its slot a moment after it joins, so the records
// read as the last machine came in can lack it: the stagenet create run printed
// four nameservers of five, and an operator who created only those would have
// waited for a delegation that could never match. The wait is bounded; past it
// the records the cluster has are printed, and the final wait prints them again.
func (r *runner) announceAllNameservers(ctx context.Context) {
	if r.opts.Domain == "" || r.d.Domain == nil || r.via == nil {
		return
	}
	want := make([]string, 0, len(r.runs))
	for _, n := range r.runs {
		want = append(want, n.plan.IP)
	}
	var readErr error
	err := pollUntil(ctx, r.d.Timing.SlotPoll, r.d.Timing.SlotDeadline, "every machine's nameserver slot", func(ctx context.Context) (bool, error) {
		have, err := r.d.Domain.NameserverIPs(ctx, r.via, r.opts.Domain)
		// A read that fails is retried until the deadline: a node that has just joined can be a
		// moment from answering. The last failure is kept for the message.
		readErr = err
		if err != nil {
			return false, nil
		}
		return allNameservers(want, have), nil
	})
	if err != nil {
		err = errors.Join(err, readErr)
		r.d.Report.Linef("  not every machine has its nameserver slot yet (%v); these are the records the cluster has, and the records it needs are printed again if DNS is not ready at the end", err)
	}
	r.announceDomain(ctx)
}

// allNameservers says have holds every address of want, or the cluster's slots are all taken.
func allNameservers(want, have []string) bool {
	if len(have) >= nameserverSlots {
		return true
	}
	for _, ip := range want {
		if !slices.Contains(have, ip) {
			return false
		}
	}
	return true
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
		r.announceDomain(ctx)
		if errors.As(err, new(*issuanceRefused)) {
			return fmt.Errorf("%s has no certificate: %w\n  everything else is installed; run `%s` once it can be issued, which resumes at this step",
				r.opts.Domain, err, r.resumeCommand())
		}
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
