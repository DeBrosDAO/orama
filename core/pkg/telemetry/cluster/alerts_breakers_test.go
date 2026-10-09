package cluster

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func breaker(ns, node string) report.BreakerReport {
	return report.BreakerReport{Namespace: ns, Node: node, State: report.BreakerOpen, Failures: 5, LastError: "connection refused"}
}

func TestCheckNodeBreakers_noneNotClosed(t *testing.T) {
	if alerts := checkNodeBreakers(&report.NodeReport{}, "10.0.0.1"); len(alerts) != 0 {
		t.Fatalf("a node with no breaker report alerted: %v", alerts)
	}
	r := &report.NodeReport{Breakers: &report.BreakersReport{Tracked: 9}}
	if alerts := checkNodeBreakers(r, "10.0.0.1"); len(alerts) != 0 {
		t.Fatalf("a node whose breakers are all closed alerted: %v", alerts)
	}
}

// A dead peer opens one breaker per namespace; that is one alert about the
// peer, not one per tenant.
func TestCheckNodeBreakers_oneAlertPerNodeNamingTheNamespaces(t *testing.T) {
	r := &report.NodeReport{Breakers: &report.BreakersReport{Tracked: 20, NotClosed: 7}}
	for i := 0; i < 6; i++ {
		r.Breakers.Unhealthy = append(r.Breakers.Unhealthy, breaker(fmt.Sprintf("ns%d", i), "10.0.0.2"))
	}
	r.Breakers.Unhealthy = append(r.Breakers.Unhealthy, breaker("acme", "10.0.0.3"))

	alerts := checkNodeBreakers(r, "10.0.0.1")
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want one per node: %v", len(alerts), alerts)
	}
	for _, a := range alerts {
		if a.Severity != AlertWarning || a.Subsystem != "namespace" || a.Node != "10.0.0.1" {
			t.Errorf("alert = %+v, want a namespace warning about the reporting node", a)
		}
	}
	if !strings.Contains(alerts[0].Message, "10.0.0.2") || !strings.Contains(alerts[0].Message, "and 1 more") ||
		!strings.Contains(alerts[0].Message, "connection refused") {
		t.Errorf("first alert = %q, want the node, the namespaces cut at five, and the reason", alerts[0].Message)
	}
	if !strings.Contains(alerts[1].Message, "acme") || !strings.Contains(alerts[1].Message, "10.0.0.3") {
		t.Errorf("second alert = %q, want acme on 10.0.0.3", alerts[1].Message)
	}
}

// The report lists at most report.MaxBreakersReported; the alert says when
// there are more than it can name.
func TestCheckNodeBreakers_saysWhenTheReportIsTruncated(t *testing.T) {
	r := &report.NodeReport{Breakers: &report.BreakersReport{
		Tracked: 300, NotClosed: 250, Unhealthy: []report.BreakerReport{breaker("acme", "10.0.0.2")},
	}}
	alerts := checkNodeBreakers(r, "10.0.0.1")
	if len(alerts) != 2 || !strings.Contains(alerts[1].Message, "249 more") {
		t.Fatalf("alerts = %v, want one for acme and one counting the 249 not listed", alerts)
	}
}
