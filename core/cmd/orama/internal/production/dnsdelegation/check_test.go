package dnsdelegation

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

var twoSlots = Delegation{
	Domain: "stage.example.test",
	Nameservers: []Nameserver{
		{Hostname: "ns1", IP: "203.0.113.5"},
		{Hostname: "ns2", IP: "203.0.113.6"},
	},
}

// answers builds Lookups from fixed NS and host tables. A name absent from
// the table is "no such record", as a resolver answers before delegation.
func answers(ns []string, hosts map[string][]string) Lookups {
	notFound := func(name string) error { return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true} }
	return Lookups{
		NS: func(_ context.Context, name string) ([]string, error) {
			if len(ns) == 0 {
				return nil, notFound(name)
			}
			return ns, nil
		},
		Host: func(_ context.Context, name string) ([]string, error) {
			h, ok := hosts[name]
			if !ok {
				return nil, notFound(name)
			}
			return h, nil
		},
	}
}

func TestCheck_correctDelegationHasNoFindings(t *testing.T) {
	l := answers(
		[]string{"ns1.stage.example.test.", "NS2.stage.example.test"},
		map[string][]string{
			"ns1.stage.example.test": {"203.0.113.5"},
			"ns2.stage.example.test": {"203.0.113.6", "2001:db8::6"},
		})
	findings, err := Check(context.Background(), twoSlots, l)
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings = %v, err = %v; want a clean delegation", findings, err)
	}
}

func TestCheck_beforeAnyRecordExistsEveryRecordIsMissing(t *testing.T) {
	findings, err := Check(context.Background(), twoSlots, answers(nil, nil))
	if err != nil {
		t.Fatalf("a name with no records is a finding, not an error: %v", err)
	}
	kinds := map[string]int{}
	for _, f := range findings {
		kinds[f.Kind]++
	}
	if kinds[FindingMissingNS] != 2 || kinds[FindingMissingGlue] != 2 || len(findings) != 4 {
		t.Fatalf("findings = %v, want two missing NS and two missing glue", findings)
	}
}

func TestCheck_oneMissingRecordNamesOnlyThatRecord(t *testing.T) {
	l := answers(
		[]string{"ns1.stage.example.test."},
		map[string][]string{
			"ns1.stage.example.test": {"203.0.113.5"},
			"ns2.stage.example.test": {"203.0.113.6"},
		})
	findings, err := Check(context.Background(), twoSlots, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Kind != FindingMissingNS || findings[0].Want != "ns2.stage.example.test" {
		t.Fatalf("findings = %v, want only ns2 missing from the NS set", findings)
	}
}

func TestCheck_glueAtTheWrongTargetIsNamed(t *testing.T) {
	l := answers(
		[]string{"ns1.stage.example.test.", "ns2.stage.example.test."},
		map[string][]string{
			"ns1.stage.example.test": {"203.0.113.5"},
			"ns2.stage.example.test": {"198.51.100.9"},
		})
	findings, err := Check(context.Background(), twoSlots, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Kind != FindingWrongGlue {
		t.Fatalf("findings = %v, want one wrong-glue", findings)
	}
	line := findings[0].String()
	if !strings.Contains(line, "198.51.100.9") || !strings.Contains(line, "203.0.113.6") {
		t.Fatalf("finding line %q does not say what DNS returned and what the cluster holds", line)
	}
}

func TestCheck_aResolverThatCannotAnswerIsAnErrorNotAFinding(t *testing.T) {
	l := Lookups{
		NS: func(context.Context, string) ([]string, error) {
			return nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}
		},
	}
	findings, err := Check(context.Background(), twoSlots, l)
	if err == nil || len(findings) != 0 {
		t.Fatalf("findings = %v, err = %v; a timeout says nothing about the records", findings, err)
	}
}

func TestCheckAll_reportsEachDomainAndKeepsAResolverFailureApart(t *testing.T) {
	failing := Lookups{NS: func(context.Context, string) ([]string, error) { return nil, errors.New("servfail") }}
	reports := CheckAll(context.Background(), []Delegation{twoSlots}, failing)
	if len(reports) != 1 || reports[0].CheckError == "" || reports[0].Delegated || len(reports[0].Findings) != 0 {
		t.Fatalf("reports = %+v, want an unverified report with a check error", reports)
	}
	ok := answers(
		[]string{"ns1.stage.example.test.", "ns2.stage.example.test."},
		map[string][]string{
			"ns1.stage.example.test": {"203.0.113.5"},
			"ns2.stage.example.test": {"203.0.113.6"},
		})
	reports = CheckAll(context.Background(), []Delegation{twoSlots}, ok)
	if !reports[0].Delegated || reports[0].CheckError != "" {
		t.Fatalf("reports = %+v, want delegated", reports)
	}
}
