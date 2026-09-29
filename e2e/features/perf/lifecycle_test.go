//go:build e2e_fleet

package perf

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	provisionBound = ns.ReadyBudget
	deployBound    = 3 * time.Minute
	coldBound      = 15 * time.Second
	warmBound      = 2 * time.Second
	warmWorkers    = 5
	warmInvokes    = 100
	dnsWorkers     = 4
	dnsQueries     = 150
	dnsBound       = 500 * time.Millisecond
)

// TestPerf_namespaceProvision: from the create request until the namespace
// is ready and its gateway answers a real request (ns.New's definition).
func TestPerf_namespaceProvision(t *testing.T) {
	f := harness.Fleet(t)
	tenancy.Reserve(t, harness.Fleet(t), 1)
	start := time.Now()
	ns.New(t, f, ns.Options{})
	record(t, f, once("namespace-provision", start, nil), provisionBound)
}

// TestPerf_deployStatic: from `orama deploy static` until the site serves the
// new content by name.
func TestPerf_deployStatic(t *testing.T) {
	tn := realistic.NewTenant(t)
	marker := "perf-" + tn.N.Name
	start := time.Now()
	u := tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: marker}, nil), "perfsite")
	realistic.Serving(t, tn.App(u), "/", marker)
	record(t, tn.F, once("deploy-static", start, nil), deployBound)
}

// TestPerf_functionColdAndWarm: the first invoke after a deploy (the
// gateway compiles the module) and then warm invokes of the same function.
func TestPerf_functionColdAndWarm(t *testing.T) {
	realistic.RequireTinyGo(t)
	tn := realistic.NewTenant(t)
	tn.DeployFunction(t, "perf-store", "store", false)
	start := time.Now()
	_, err := realistic.Invoke(t.Context(), tn.C, "perf-store", tn.Admin.Bearer, map[string]string{"op": "count"})
	record(t, tn.F, once("function-cold-invoke", start, err), coldBound)
	warm := realistic.Burst(t.Context(), warmWorkers, warmInvokes, func(ctx context.Context, _, _ int) error {
		_, err := realistic.Invoke(ctx, tn.C, "perf-store", tn.Admin.Bearer, map[string]string{"op": "count"})
		return err
	})
	record(t, tn.F, realistic.Summarize("function-warm-invoke", warm, nil), warmBound)
}

// TestPerf_dnsAnswer: A queries for the base domain asked of each
// nameserver directly, round robin, bypassing every resolver cache.
func TestPerf_dnsAnswer(t *testing.T) {
	f := harness.Fleet(t)
	servers := tenancy.Nameservers(f)
	samples := realistic.Burst(t.Context(), dnsWorkers, dnsQueries, func(ctx context.Context, w, i int) error {
		s := servers[(w+i)%len(servers)]
		addrs, err := tenancy.ResolveAt(ctx, s.PublicIP, f.State.BaseDomain)
		if err == nil && len(addrs) == 0 {
			err = fmt.Errorf("%s answered no A record for %s", s.Name, f.State.BaseDomain)
		}
		return err
	})
	record(t, f, realistic.Summarize("dns-answer", samples, nil), dnsBound)
}
