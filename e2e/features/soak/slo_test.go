//go:build e2e_fleet

package soak

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// SLOs. The error budget and latency bounds hold outside the injected
// windows; growth bounds compare the start and the end of the soak.
const (
	maxErrorRate     = 0.01
	p95Bound         = 2 * time.Second
	uploadP95Bound   = 5 * time.Second
	minDelivery      = 0.99
	memGrowthPct     = 50
	memGrowthFloorMB = 64 // below this a daemon's growth is allocator noise
	fdGrowthPct      = 50
	fdGrowthFloor    = 64
)

// reading is one daemon's footprint at one moment.
type reading struct {
	MemMB    int `json:"mem_mb"`
	FDs      int `json:"fds"`
	Restarts int `json:"restarts"`
}

// watched are the daemons whose footprint the soak bounds, by node.
func watched(f *fleet.Fleet, namespace string) map[string][]string {
	nameservers := map[string]bool{}
	for _, n := range tenancy.Nameservers(f) {
		nameservers[n.Name] = true
	}
	out := map[string][]string{}
	for _, n := range f.State.Nodes {
		units := tenancy.TenantUnits(namespace)
		for _, sc := range realistic.ServiceClasses {
			if !sc.NameserverOnly || nameservers[n.Name] {
				units = append(units, sc.Unit)
			}
		}
		out[n.Name] = units
	}
	return out
}

// readAll reads every watched daemon's footprint.
func readAll(t *testing.T, f *fleet.Fleet, units map[string][]string) map[string]reading {
	t.Helper()
	out := map[string]reading{}
	for _, n := range f.State.Nodes {
		for _, u := range units[n.Name] {
			restarts, err := strconv.Atoi(strings.TrimSpace(f.MustExec(t, n, "systemctl show -p NRestarts --value "+u).Stdout))
			if err != nil {
				t.Fatalf("%s: NRestarts of %s: %v", n.Name, u, err)
			}
			out[n.Name+"/"+u] = reading{MemMB: realistic.MemoryCurrentMB(t, f, n, u), FDs: realistic.FDCount(t, f, n, u), Restarts: restarts}
		}
	}
	return out
}

// checkFootprint fails a daemon that grew past its bounds, or restarted
// more often than the faults that targeted it explain (a crash loop).
func checkFootprint(t *testing.T, before, after map[string]reading, killed map[string]int) {
	t.Helper()
	for key, b := range before {
		a, ok := after[key]
		if !ok {
			t.Errorf("%s: no reading at the end", key)
			continue
		}
		if grew(b.MemMB, a.MemMB, memGrowthPct, memGrowthFloorMB) && killed[key] == 0 {
			t.Errorf("%s: memory grew %d -> %d MiB (over %d%%)", key, b.MemMB, a.MemMB, memGrowthPct)
		}
		if grew(b.FDs, a.FDs, fdGrowthPct, fdGrowthFloor) && killed[key] == 0 {
			t.Errorf("%s: file descriptors grew %d -> %d (over %d%%)", key, b.FDs, a.FDs, fdGrowthPct)
		}
		if extra := a.Restarts - b.Restarts - killed[key]; extra > 0 {
			t.Errorf("%s restarted %d time(s) no fault explains", key, extra)
		}
	}
}

func grew(before, after, pct, floor int) bool {
	return after-before > floor && after > before*(100+pct)/100
}

// checkTraffic summarizes every op outside and inside the windows, records
// both, and holds the outside numbers to the SLOs.
func checkTraffic(t *testing.T, samples map[string][]realistic.Sample, ws *windows) map[string]realistic.Summary {
	t.Helper()
	out := map[string]realistic.Summary{}
	for name, list := range samples {
		clean := realistic.Summarize(name, list, func(s realistic.Sample) bool { return !ws.covers(s.At) })
		out[name] = clean
		out[name+"@injected"] = realistic.Summarize(name+"@injected", list, func(s realistic.Sample) bool { return ws.covers(s.At) })
		if clean.Count == 0 {
			t.Errorf("%s: no operation outside the injected windows", name)
			continue
		}
		if clean.ErrorRate > maxErrorRate {
			t.Errorf("%s: error rate %.2f%% over %.0f%% outside the injected windows (first: %s)", name, 100*clean.ErrorRate, 100*maxErrorRate, clean.FirstError)
		}
		bound := p95Bound
		if name == "storage-upload" {
			bound = uploadP95Bound
		}
		if clean.P95MS > float64(bound.Milliseconds()) {
			t.Errorf("%s: p95 %.0fms over %s", name, clean.P95MS, bound)
		}
	}
	return out
}

// checkSockets fails a socket lost outside the windows (other than the
// documented expiry close) and a subscriber that missed messages published
// while it was connected, outside the windows.
func checkSockets(t *testing.T, subs []*subscriber, fd *feed, ws *windows, end time.Time) map[string]any {
	t.Helper()
	fd.mu.Lock()
	published := make(map[int64]time.Time, len(fd.at))
	for k, v := range fd.at {
		published[k] = v
	}
	fd.mu.Unlock()
	report := map[string]any{}
	for _, s := range subs {
		s.mu.Lock()
		want, got := 0, 0
		for _, c := range s.conns {
			if c.Code != closeExpired && !c.Close.IsZero() && c.Close.Before(end) && !ws.covers(c.Close) {
				t.Errorf("%s: socket lost at %s outside any fault (code %d): %s", s.name, c.Close.Format(time.RFC3339), c.Code, c.Err)
			}
			w, g := delivered(c, published, s.got, ws, end)
			want, got = want+w, got+g
		}
		report[s.name] = map[string]any{"connections": len(s.conns), "expected": want, "received": got, "conns": s.conns}
		s.mu.Unlock()
		if want > 0 && float64(got)/float64(want) < minDelivery {
			t.Errorf("%s received %d of %d messages published while it was connected (under %.0f%%)", s.name, got, want, 100*minDelivery)
		}
	}
	return report
}

// delivered counts the messages c should have received and did.
func delivered(c conn, published map[int64]time.Time, got map[int64]bool, ws *windows, end time.Time) (want, have int) {
	closeAt := c.Close
	if closeAt.IsZero() {
		closeAt = end
	}
	from, to := c.Open.Add(joinGrace), closeAt.Add(-deliverGrace)
	for seq, at := range published {
		if at.After(from) && at.Before(to) && !ws.covers(at) {
			want++
			if got[seq] {
				have++
			}
		}
	}
	return want, have
}
