package dnsdelegation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Finding kinds, in the order an operator fixes them.
const (
	// FindingMissingNS means the domain's NS set does not name a nameserver.
	FindingMissingNS = "missing-ns"
	// FindingMissingGlue means a nameserver's own name does not resolve.
	FindingMissingGlue = "missing-glue"
	// FindingWrongGlue means a nameserver's name resolves, but not to the
	// address the cluster holds that slot at.
	FindingWrongGlue = "wrong-glue"
)

// Finding is one record the outside world does not return as the cluster
// expects: what is wrong, which record, what the cluster wants and what DNS
// answered.
type Finding struct {
	Kind   string   `json:"kind"`
	Record string   `json:"record"`
	Want   string   `json:"want"`
	Got    []string `json:"got,omitempty"`
}

// String is the line printed for the operator.
func (f Finding) String() string {
	got := "nothing"
	if len(f.Got) > 0 {
		got = strings.Join(f.Got, ", ")
	}
	switch f.Kind {
	case FindingMissingNS:
		return fmt.Sprintf("NS for %s does not include %s (got %s)", f.Record, f.Want, got)
	case FindingMissingGlue:
		return fmt.Sprintf("glue %s does not resolve; it must be %s", f.Record, f.Want)
	default:
		return fmt.Sprintf("glue %s is %s, want %s", f.Record, got, f.Want)
	}
}

// Lookups are the two DNS questions a check asks. A nil field uses the system
// resolver; tests and the Cloudflare path set their own.
type Lookups struct {
	NS   func(ctx context.Context, name string) ([]string, error)
	Host func(ctx context.Context, name string) ([]string, error)
}

func (l Lookups) withDefaults() Lookups {
	if l.NS == nil {
		l.NS = func(ctx context.Context, name string) ([]string, error) {
			hosts, err := net.DefaultResolver.LookupNS(ctx, name)
			out := make([]string, len(hosts))
			for i, h := range hosts {
				out[i] = h.Host
			}
			return out, err
		}
	}
	if l.Host == nil {
		l.Host = net.DefaultResolver.LookupHost
	}
	return l
}

// Check asks DNS whether d is delegated: the domain's NS set names every
// nameserver and each nameserver's name resolves to the address the cluster
// holds it at. An answer that is simply "no such record" is a finding, because
// before the operator has created the records that is the expected state.
// A resolver that cannot answer (timeout, SERVFAIL) is an error: it says
// nothing about the records.
func Check(ctx context.Context, d Delegation, l Lookups) ([]Finding, error) {
	l = l.withDefaults()
	gotNS, err := lookup(ctx, l.NS, d.Domain)
	if err != nil {
		return nil, fmt.Errorf("look up NS for %s: %w", d.Domain, err)
	}
	var findings []Finding
	for _, ns := range d.Nameservers {
		fqdn := ns.Hostname + "." + d.Domain
		if !containsName(gotNS, fqdn) {
			findings = append(findings, Finding{Kind: FindingMissingNS, Record: d.Domain, Want: fqdn, Got: gotNS})
		}
		hosts, err := lookup(ctx, l.Host, fqdn)
		if err != nil {
			return nil, fmt.Errorf("look up glue %s: %w", fqdn, err)
		}
		switch {
		case len(hosts) == 0:
			findings = append(findings, Finding{Kind: FindingMissingGlue, Record: fqdn, Want: ns.IP})
		case !containsName(hosts, ns.IP):
			findings = append(findings, Finding{Kind: FindingWrongGlue, Record: fqdn, Want: ns.IP, Got: hosts})
		}
	}
	return findings, nil
}

// lookup runs one DNS question and turns "the name has no such records" into
// an empty answer. Every other failure stays an error.
func lookup(ctx context.Context, fn func(context.Context, string) ([]string, error), name string) ([]string, error) {
	out, err := fn(ctx, name)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Report is a delegation and what DNS says about it.
type Report struct {
	Delegation
	// Delegated is true when the parent zone returns every record.
	Delegated bool `json:"delegated"`
	// Findings are the records that are missing or point elsewhere.
	Findings []Finding `json:"findings,omitempty"`
	// CheckError is set when DNS could not be asked (the resolver timed out
	// or failed). Delegated is false and Findings is empty then: nothing was
	// learned about the records.
	CheckError string `json:"check_error,omitempty"`
}

// CheckAll checks every delegation, one report each.
func CheckAll(ctx context.Context, ds []Delegation, l Lookups) []Report {
	reports := make([]Report, 0, len(ds))
	for _, d := range ds {
		r := Report{Delegation: d}
		findings, err := Check(ctx, d, l)
		if err != nil {
			r.CheckError = err.Error()
		} else {
			r.Findings = findings
			r.Delegated = len(findings) == 0
		}
		reports = append(reports, r)
	}
	return reports
}
