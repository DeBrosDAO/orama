package provision

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

func TestDown_removesEverythingAndIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	st, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err != nil {
		t.Fatal(err)
	}
	st.Extras = nil // an extra dropped from the state is still found by its label
	e.cloud.addServer("e2e-testrun1-extra", map[string]string{hetzner.LabelRun: "testrun1"}, time.Now())
	if err := down(context.Background(), st, &testLogger{}, e.d); err != nil {
		t.Fatalf("down: %v", err)
	}
	assertTornDown(t, e)
	if _, err := os.Stat(st.Home); !os.IsNotExist(err) {
		t.Fatalf("the agent home survived: %v", err)
	}
	if err := down(context.Background(), st, &testLogger{}, e.d); err != nil {
		t.Fatalf("a second down: %v", err)
	}
}

func TestDown_leavesOtherRunsAlone(t *testing.T) {
	e := newTestEnv(t)
	other := e.cloud.addServer("e2e-otherrun-n1", map[string]string{hetzner.LabelRun: "otherrun"}, time.Now())
	e.dns.add("NS", "e2e-otherrun."+testZone, "ns1.e2e-otherrun."+testZone, time.Now())
	st := &fleet.State{RunID: "testrun1"}
	if err := down(context.Background(), st, &testLogger{}, e.d); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := e.cloud.counts(); s != 1 || len(e.dns.records) != 1 {
		t.Fatalf("down touched another run (server %d kept: %d servers, %d records)", other.ID, s, len(e.dns.records))
	}
}

func TestDown_refusesStateWithoutRunID(t *testing.T) {
	e := newTestEnv(t)
	for _, st := range []*fleet.State{nil, {}, {RunID: "Bad Id"}} {
		if err := down(context.Background(), st, &testLogger{}, e.d); err == nil {
			t.Errorf("down(%+v) went ahead", st)
		}
	}
}

func TestDown_refusesAForeignBaseDomain(t *testing.T) {
	e := newTestEnv(t)
	e.dns.add("A", "www."+testZone, "203.0.113.9", time.Now())
	st := &fleet.State{RunID: "testrun1", BaseDomain: testZone}
	if err := down(context.Background(), st, &testLogger{}, e.d); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("down with the zone apex as base domain: %v", err)
	}
	if len(e.dns.records) != 1 {
		t.Fatal("down deleted a record outside the run")
	}
}

func TestDown_keepsGoingPastAFailure(t *testing.T) {
	e := newTestEnv(t)
	e.cloud.failList = true
	e.dns.add("NS", "e2e-testrun1."+testZone, "ns1.e2e-testrun1."+testZone, time.Now())
	home := t.TempDir()
	st := &fleet.State{RunID: "testrun1", Home: home}
	err := down(context.Background(), st, &testLogger{}, e.d)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("down with Hetzner failing: %v", err)
	}
	if len(e.dns.records) != 0 {
		t.Fatal("a Hetzner failure stopped the DNS cleanup")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("a Hetzner failure stopped the agent cleanup")
	}
}

func TestSweep_expiredAndOrphaned(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	old := now.Add(-10 * time.Hour)
	e.cloud.addServer("e2e-dead-n1", map[string]string{hetzner.LabelRun: "deadrun1", hetzner.LabelTTL: "6h0m0s"}, old)
	e.cloud.addServer("e2e-live-n1", map[string]string{hetzner.LabelRun: "liverun1", hetzner.LabelTTL: "6h0m0s"}, now)
	e.cloud.addServer("e2e-short-n1", map[string]string{hetzner.LabelRun: "shortrun", hetzner.LabelTTL: "1h0m0s"}, now.Add(-2*time.Hour))
	e.cloud.addServer("someone-else", map[string]string{}, old)
	pastGrace := now.Add(-dnsGrace - time.Minute)
	e.dns.add("NS", "e2e-liverun1."+testZone, "ns1.e2e-liverun1."+testZone, pastGrace)
	e.dns.add("NS", "e2e-deadrun1."+testZone, "ns1.e2e-deadrun1."+testZone, pastGrace)
	e.dns.add("NS", "e2e-ghostrun."+testZone, "ns1.e2e-ghostrun."+testZone, pastGrace)
	e.dns.add("A", "www."+testZone, "203.0.113.9", old)
	removed, err := sweep(context.Background(), 8*time.Hour, &testLogger{}, e.d, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := strings.Join(removed, "\n")
	for _, want := range []string{"e2e-dead-n1", "e2e-short-n1", "e2e-deadrun1", "e2e-ghostrun"} {
		if !strings.Contains(got, want) {
			t.Errorf("sweep did not remove %s:\n%s", want, got)
		}
	}
	for _, keep := range []string{"e2e-live-n1", "someone-else", "e2e-liverun1", "www."} {
		if strings.Contains(got, keep) {
			t.Errorf("sweep removed %s", keep)
		}
	}
}

func TestSweep_serverListFailureSkipsDNS(t *testing.T) {
	e := newTestEnv(t)
	e.cloud.failList = true
	e.dns.add("NS", "e2e-liverun1."+testZone, "ns1.e2e-liverun1."+testZone, time.Now())
	if _, err := sweep(context.Background(), time.Hour, &testLogger{}, e.d, time.Now()); err == nil {
		t.Fatal("sweep hid a Hetzner failure")
	}
	if len(e.dns.records) != 1 {
		t.Fatal("sweep deleted DNS without knowing which runs are live")
	}
}

func TestSweep_refusesNonPositiveAge(t *testing.T) {
	e := newTestEnv(t)
	if _, err := sweep(context.Background(), 0, &testLogger{}, e.d, time.Now()); err == nil {
		t.Fatal("sweep with max age 0 ran")
	}
}

func TestExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		labels map[string]string
		age    time.Duration
		want   bool
	}{
		{map[string]string{}, time.Hour, false},
		{map[string]string{}, 3 * time.Hour, true},
		{map[string]string{hetzner.LabelTTL: "30m0s"}, time.Hour, true},
		{map[string]string{hetzner.LabelTTL: "garbage"}, time.Hour, false},
	}
	for _, c := range cases {
		if got := expired(c.labels, now.Add(-c.age), now, 2*time.Hour); got != c.want {
			t.Errorf("expired(%v, age %s) = %v", c.labels, c.age, got)
		}
	}
}
