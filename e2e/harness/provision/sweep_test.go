package provision

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

func TestSweep_keepsDNSOfARunThatStartedMidSweep(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	e.dns.add("NS", "e2e-newrun1."+testZone, "ns1.e2e-newrun1."+testZone, now.Add(-dnsGrace-time.Minute))
	// The run's server appears only after the sweep's first listing.
	e.cloud.beforeList = func(n int) {
		if n == 2 {
			e.cloud.addServer("e2e-newrun1-n1", map[string]string{hetzner.LabelRun: "newrun1", hetzner.LabelTTL: "6h0m0s"}, now)
		}
	}
	removed, err := sweep(context.Background(), 8*time.Hour, &testLogger{}, e.d, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(removed, "\n"), "e2e-newrun1") || len(e.dns.records) != 1 {
		t.Fatalf("the sweep deleted the delegation of a run that started while it ran: %v", removed)
	}
}

func TestSweep_keepsYoungAndFutureRecords(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	e.dns.add("NS", "e2e-young001."+testZone, "ns1.e2e-young001."+testZone, now.Add(-time.Minute))
	e.dns.add("NS", "e2e-later001."+testZone, "ns1.e2e-later001."+testZone, now.Add(time.Minute))
	if _, err := sweep(context.Background(), 8*time.Hour, &testLogger{}, e.d, now); err != nil {
		t.Fatal(err)
	}
	if len(e.dns.records) != 2 {
		t.Fatalf("the sweep judged records inside the grace period: %+v", e.dns.records)
	}
}

func TestSweep_keepsAccessOfALiveRun(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	labels := map[string]string{hetzner.LabelRun: "liverun1", hetzner.LabelTTL: "6h0m0s"}
	e.cloud.addServer("e2e-liverun1-n1", labels, now)
	old := now.Add(-10 * time.Hour)
	e.cloud.keys[900] = hetzner.SSHKey{ID: 900, Name: "e2e-liverun1", Labels: labels, Created: old}
	e.cloud.firewalls[901] = hetzner.Firewall{ID: 901, Name: "e2e-liverun1", Labels: labels, Created: old}
	e.cloud.keys[902] = hetzner.SSHKey{ID: 902, Name: "e2e-deadrun1", Labels: map[string]string{hetzner.LabelRun: "deadrun1"}, Created: old}
	removed, err := sweep(context.Background(), 8*time.Hour, &testLogger{}, e.d, now)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(removed, "\n")
	if strings.Contains(got, "e2e-liverun1") {
		t.Fatalf("the sweep deleted the firewall or SSH key of a live run:\n%s", got)
	}
	if !strings.Contains(got, "e2e-deadrun1") {
		t.Fatalf("the sweep kept the SSH key of a dead run:\n%s", got)
	}
}

func TestSweep_serverListFailureSkipsAccess(t *testing.T) {
	e := newTestEnv(t)
	e.cloud.failList = true
	e.cloud.keys[900] = hetzner.SSHKey{ID: 900, Name: "e2e-x", Labels: map[string]string{hetzner.LabelRun: "xrun0001"}, Created: time.Now().Add(-10 * time.Hour)}
	if _, err := sweep(context.Background(), time.Hour, &testLogger{}, e.d, time.Now()); err == nil {
		t.Fatal("sweep hid a Hetzner failure")
	}
	if _, k, _ := e.cloud.counts(); k != 1 {
		t.Fatal("sweep deleted SSH keys without knowing which runs are live")
	}
}

// TestSweep_neverUndercutsALiveRun: a run nine hours in, inside its
// twelve-hour e2e-ttl, keeps its servers, access and records under a sweep
// with a one-hour max age.
func TestSweep_neverUndercutsALiveRun(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	started := now.Add(-9 * time.Hour)
	labels := map[string]string{hetzner.LabelRun: "longrun1", hetzner.LabelTTL: "12h0m0s"}
	e.cloud.addServer("e2e-longrun1-n1", labels, started)
	e.cloud.keys[900] = hetzner.SSHKey{ID: 900, Name: "e2e-longrun1", Labels: labels, Created: started}
	e.dns.add("NS", "e2e-longrun1."+testZone, "ns1.e2e-longrun1."+testZone, started)
	removed, err := sweep(context.Background(), time.Hour, &testLogger{}, e.d, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("the sweep took a live run:\n%s", strings.Join(removed, "\n"))
	}
}

// TestSweep_onlyRunNamedAndRunLabelled: a server carrying the e2e-run label
// but not named e2e-..., or labelled with something that is not a run id,
// is never deleted.
func TestSweep_onlyRunNamedAndRunLabelled(t *testing.T) {
	e := newTestEnv(t)
	old := time.Now().Add(-48 * time.Hour)
	e.cloud.addServer("prod-db-1", map[string]string{hetzner.LabelRun: "deadrun1"}, old)
	e.cloud.addServer("e2e-weird", map[string]string{hetzner.LabelRun: "Not_A_Run"}, old)
	e.cloud.addServer("e2e-deadrun1-n1", map[string]string{hetzner.LabelRun: "deadrun1"}, old)
	removed, err := sweep(context.Background(), time.Hour, &testLogger{}, e.d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(removed, "\n")
	if strings.Contains(got, "prod-db-1") || strings.Contains(got, "e2e-weird") || !strings.Contains(got, "e2e-deadrun1-n1") {
		t.Fatalf("removed:\n%s", got)
	}
}
