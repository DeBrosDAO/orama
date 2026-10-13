package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
)

var checkDelegation = dnsdelegation.Delegation{
	Domain:      "stage.example.test",
	Nameservers: []dnsdelegation.Nameserver{{Hostname: "ns1", IP: "203.0.113.5"}},
}

// recordInto replaces the environment store with a slice for one test.
func recordInto(t *testing.T) *[]cli.DelegationStatus {
	t.Helper()
	var stored []cli.DelegationStatus
	orig := recordDelegation
	recordDelegation = func(env string, s cli.DelegationStatus) error {
		if env != "devnet" {
			t.Errorf("recorded under %q, want devnet", env)
		}
		stored = append(stored, s)
		return nil
	}
	t.Cleanup(func() { recordDelegation = orig })
	return &stored
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func TestCheckAndRecord_beforeDelegationStoresWhatIsMissing(t *testing.T) {
	stored := recordInto(t)
	var out, errOut bytes.Buffer
	l := dnsdelegation.Lookups{
		NS:   func(_ context.Context, name string) ([]string, error) { return nil, notFound(name) },
		Host: func(_ context.Context, name string) ([]string, error) { return nil, notFound(name) },
	}
	if err := checkAndRecord(printer.New(&out, &errOut), "devnet", []dnsdelegation.Delegation{checkDelegation}, l); err != nil {
		t.Fatal(err)
	}
	if len(*stored) != 1 || (*stored)[0].Delegated || len((*stored)[0].Findings) != 2 {
		t.Fatalf("stored = %+v, want one undelegated result with two findings", *stored)
	}
	if !strings.Contains(errOut.String(), "not delegated yet") || !strings.Contains(out.String(), "ns1.stage.example.test") {
		t.Fatalf("output does not say what is missing:\nstdout: %s\nstderr: %s", out.String(), errOut.String())
	}
}

func TestCheckAndRecord_delegatedIsStoredAndPrinted(t *testing.T) {
	stored := recordInto(t)
	var out, errOut bytes.Buffer
	l := dnsdelegation.Lookups{
		NS:   func(context.Context, string) ([]string, error) { return []string{"ns1.stage.example.test."}, nil },
		Host: func(context.Context, string) ([]string, error) { return []string{"203.0.113.5"}, nil },
	}
	if err := checkAndRecord(printer.New(&out, &errOut), "devnet", []dnsdelegation.Delegation{checkDelegation}, l); err != nil {
		t.Fatal(err)
	}
	if len(*stored) != 1 || !(*stored)[0].Delegated || len((*stored)[0].Findings) != 0 {
		t.Fatalf("stored = %+v, want a delegated result", *stored)
	}
	if !strings.Contains(out.String(), "DNS returns every record") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestCheckAndRecord_aResolverFailureStoresNothing(t *testing.T) {
	stored := recordInto(t)
	var out, errOut bytes.Buffer
	l := dnsdelegation.Lookups{NS: func(context.Context, string) ([]string, error) { return nil, errors.New("servfail") }}
	if err := checkAndRecord(printer.New(&out, &errOut), "devnet", []dnsdelegation.Delegation{checkDelegation}, l); err != nil {
		t.Fatal(err)
	}
	if len(*stored) != 0 {
		t.Fatalf("stored %+v after a check that learned nothing", *stored)
	}
	if !strings.Contains(errOut.String(), "unverified") {
		t.Fatalf("stderr = %q, want the unverified warning", errOut.String())
	}
}

func TestCheckAndRecord_jsonKeepsTheDelegationShapeAndAddsTheVerdict(t *testing.T) {
	recordInto(t)
	var out, errOut bytes.Buffer
	l := dnsdelegation.Lookups{
		NS:   func(_ context.Context, name string) ([]string, error) { return nil, notFound(name) },
		Host: func(_ context.Context, name string) ([]string, error) { return nil, notFound(name) },
	}
	p := printer.New(&out, &errOut).WithJSON(true)
	if err := checkAndRecord(p, "devnet", []dnsdelegation.Delegation{checkDelegation}, l); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Domain      string                     `json:"domain"`
		Nameservers []dnsdelegation.Nameserver `json:"nameservers"`
		Delegated   bool                       `json:"delegated"`
		Findings    []dnsdelegation.Finding    `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].Domain != checkDelegation.Domain || len(got[0].Nameservers) != 1 || got[0].Delegated || len(got[0].Findings) != 2 {
		t.Fatalf("json = %+v", got)
	}
}

func TestCheckAndRecord_aFailedStoreIsAnError(t *testing.T) {
	orig := recordDelegation
	recordDelegation = func(string, cli.DelegationStatus) error { return errors.New("environment not configured") }
	t.Cleanup(func() { recordDelegation = orig })
	l := dnsdelegation.Lookups{
		NS:   func(context.Context, string) ([]string, error) { return []string{"ns1.stage.example.test."}, nil },
		Host: func(context.Context, string) ([]string, error) { return []string{"203.0.113.5"}, nil },
	}
	var out, errOut bytes.Buffer
	err := checkAndRecord(printer.New(&out, &errOut), "devnet", []dnsdelegation.Delegation{checkDelegation}, l)
	if err == nil || !strings.Contains(err.Error(), "store the delegation check") {
		t.Fatalf("err = %v", err)
	}
}
