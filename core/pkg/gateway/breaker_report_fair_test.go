package gateway

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func breakerStatus(ns, dep, node string) BreakerStatus {
	return BreakerStatus{Namespace: ns, Deployment: dep, Node: node, State: CircuitOpen}
}

// The report listed the first 50 breakers by namespace: one tenant with 50
// failing deployments hid every later namespace's breakers.
func TestFairBreakerSample_oneTenantCannotHideTheOthers(t *testing.T) {
	var all []BreakerStatus
	for i := 0; i < 60; i++ {
		all = append(all, breakerStatus("aaa", fmt.Sprintf("app%02d", i), "10.0.0.2"))
	}
	all = append(all, breakerStatus("bbb", "", "10.0.0.2"), breakerStatus("ccc", "shop", "10.0.0.3"), breakerStatus("ddd", "shop", "10.0.0.3"))

	got := fairBreakerSample(all, 50)
	if len(got) != 50 {
		t.Fatalf("sampled %d, want 50", len(got))
	}
	have := map[string]int{}
	for _, b := range got {
		have[b.Namespace]++
	}
	if have["bbb"] != 1 || have["ccc"] != 1 || have["ddd"] != 1 || have["aaa"] != 47 {
		t.Errorf("per namespace = %v, want bbb, ccc and ddd listed and aaa cut to 47", have)
	}
	if got[0].Namespace != "aaa" || got[len(got)-1].Namespace != "ddd" {
		t.Errorf("the sample lost the report's order: first %v last %v", got[0], got[len(got)-1])
	}
}

// A namespace gateway's breaker refuses a whole namespace; it goes before any
// deployment's.
func TestFairBreakerSample_gatewayBreakersComeFirst(t *testing.T) {
	var all []BreakerStatus
	for i := 0; i < 10; i++ {
		all = append(all, breakerStatus(fmt.Sprintf("a%02d", i), "app", "10.0.0.2"))
	}
	for i := 0; i < 4; i++ {
		all = append(all, breakerStatus(fmt.Sprintf("z%02d", i), "", "10.0.0.2"))
	}
	got := fairBreakerSample(all, 6)
	gateways := 0
	for _, b := range got {
		if b.Deployment == "" {
			gateways++
		}
	}
	if len(got) != 6 || gateways != 4 {
		t.Errorf("sampled %d with %d gateway breakers, want 6 with all 4", len(got), gateways)
	}
}

func TestFairBreakerSample_underTheLimitIsUnchanged(t *testing.T) {
	if got := fairBreakerSample(nil, 50); len(got) != 0 {
		t.Errorf("nil sampled to %v", got)
	}
	all := []BreakerStatus{breakerStatus("a", "", "n"), breakerStatus("b", "x", "n")}
	if got := fairBreakerSample(all, 50); !reflect.DeepEqual(got, all) {
		t.Errorf("got %v, want %v", got, all)
	}
}

func TestBreakersReport_oneTenantsDeploymentsDoNotHideAnotherNamespace(t *testing.T) {
	g := &Gateway{circuitBreakers: NewCircuitBreakerRegistry()}
	for i := 0; i < report.MaxBreakersReported+10; i++ {
		cb := g.circuitBreakers.ForDeployment(fmt.Sprintf("dep-%d", i), "aaa", fmt.Sprintf("app%02d", i), "10.0.0.2")
		for j := 0; j < defaultFailureThreshold; j++ {
			cb.RecordFailure("connection refused")
		}
	}
	other := g.circuitBreakers.ForNamespaceGateway("zzz", "10.0.0.3")
	for j := 0; j < defaultFailureThreshold; j++ {
		other.RecordFailure("connection refused")
	}
	r := g.breakersReport()
	if len(r.Unhealthy) != report.MaxBreakersReported || r.NotClosed != report.MaxBreakersReported+11 {
		t.Fatalf("listed %d of %d", len(r.Unhealthy), r.NotClosed)
	}
	found := false
	for _, b := range r.Unhealthy {
		found = found || b.Namespace == "zzz"
	}
	if !found {
		t.Error("zzz's open gateway breaker is missing from the report")
	}
}
