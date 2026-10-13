package gateway

import (
	"strings"
	"testing"
	"time"
)

// The reclaim's safety rests entirely on the SQL guards, so assert them
// explicitly. Losing any one of them turns a self-heal into a way for a node to
// force itself back into DNS.
func TestReclaimSQL_onlyTouchesInactiveStaleOwnRow(t *testing.T) {
	q := reclaimStaleNamespaceHostRecordSQL

	if !strings.Contains(q, "is_active = 0") {
		t.Error("missing `is_active = 0` guard — would rewrite already-active rows on every probe")
	}
	if !strings.Contains(q, "updated_at <") {
		t.Error("missing staleness guard — would override a LIVE health-monitor verdict and flap DNS")
	}
	if !strings.Contains(q, "value = ?") {
		t.Error("missing value predicate — a node could re-enable OTHER nodes' records")
	}
	if !strings.Contains(q, "fqdn = ?") {
		t.Error("missing fqdn predicate — would leak across namespaces")
	}
	if !strings.Contains(q, "record_type = 'A'") {
		t.Error("missing record_type predicate")
	}
	if !strings.HasPrefix(strings.TrimSpace(q), "UPDATE dns_records") {
		t.Errorf("reclaim must be an UPDATE on dns_records, got: %s", q)
	}
	// It must never create rows — insertion is EnsureNamespaceHostRecordForNode's
	// job, and an INSERT here would resurrect deliberately removed nodes.
	if strings.Contains(strings.ToUpper(q), "INSERT") {
		t.Error("reclaim must not INSERT; it only re-activates an existing row")
	}
}

// The window must comfortably exceed the health monitor's suspect cadence
// (DefaultSuspectAfter=3 missed probes at ProbeInterval=10s ≈ 30s). Otherwise a
// monitor that still considers this node suspect would be overridden and DNS
// would oscillate.
func TestStaleDisableReclaimAfter_exceedsSuspectCadence(t *testing.T) {
	const suspectCadence = 30 * time.Second
	if staleDisableReclaimAfter <= suspectCadence {
		t.Fatalf("staleDisableReclaimAfter = %s; must exceed the ~%s suspect cadence so a live verdict is never overridden",
			staleDisableReclaimAfter, suspectCadence)
	}
	if staleDisableReclaimAfter < 5*time.Minute {
		t.Errorf("staleDisableReclaimAfter = %s; too aggressive — leaves little margin over a slow rolling restart", staleDisableReclaimAfter)
	}
	if staleDisableReclaimAfter > time.Hour {
		t.Errorf("staleDisableReclaimAfter = %s; too slow — a node would sit out of DNS for that long after every upgrade", staleDisableReclaimAfter)
	}
}

// Damping: a namespace must be healthy for several consecutive probes before
// its node re-advertises, so a flapping service cannot flap DNS.
func TestHealthyProbesBeforeDNSReclaim_dampsFlapping(t *testing.T) {
	if healthyProbesBeforeDNSReclaim < 2 {
		t.Fatalf("healthyProbesBeforeDNSReclaim = %d; a single good probe is not enough to justify a DNS write",
			healthyProbesBeforeDNSReclaim)
	}
	if healthyProbesBeforeDNSReclaim > 10 {
		t.Errorf("healthyProbesBeforeDNSReclaim = %d; recovery would take over 5 minutes", healthyProbesBeforeDNSReclaim)
	}
}

// streak bookkeeping — extracted so the decision is testable without a DB.
func advanceStreaks(state *namespaceHealthState, health map[string]*NamespaceHealth) []string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.healthyStreak == nil {
		state.healthyStreak = make(map[string]int)
	}
	ready := []string{}
	for name, h := range health {
		if h == nil || h.Status != "healthy" {
			delete(state.healthyStreak, name)
			continue
		}
		state.healthyStreak[name]++
		if state.healthyStreak[name] >= healthyProbesBeforeDNSReclaim {
			ready = append(ready, name)
		}
	}
	for name := range state.healthyStreak {
		if _, still := health[name]; !still {
			delete(state.healthyStreak, name)
		}
	}
	return ready
}

func healthy() map[string]*NamespaceHealth {
	return map[string]*NamespaceHealth{"anchat-test": {Status: "healthy"}}
}

func unhealthy() map[string]*NamespaceHealth {
	return map[string]*NamespaceHealth{"anchat-test": {Status: "unhealthy"}}
}

func TestStreak_requiresConsecutiveHealthyProbes(t *testing.T) {
	st := &namespaceHealthState{}

	for i := 1; i < healthyProbesBeforeDNSReclaim; i++ {
		if got := advanceStreaks(st, healthy()); len(got) != 0 {
			t.Fatalf("probe %d: ready=%v; must wait for %d consecutive healthy probes", i, got, healthyProbesBeforeDNSReclaim)
		}
	}
	if got := advanceStreaks(st, healthy()); len(got) != 1 || got[0] != "anchat-test" {
		t.Fatalf("probe %d: ready=%v; want [anchat-test]", healthyProbesBeforeDNSReclaim, got)
	}
}

func TestStreak_resetsOnUnhealthyProbe(t *testing.T) {
	st := &namespaceHealthState{}

	advanceStreaks(st, healthy())
	advanceStreaks(st, healthy())
	// One bad probe wipes the streak — a flapping gateway must not reach the
	// threshold by accumulating good probes across outages.
	advanceStreaks(st, unhealthy())

	if got := advanceStreaks(st, healthy()); len(got) != 0 {
		t.Fatalf("ready=%v immediately after an unhealthy probe; streak must restart from zero", got)
	}
}

func TestStreak_forgetsNamespacesNoLongerServed(t *testing.T) {
	st := &namespaceHealthState{}
	advanceStreaks(st, healthy())

	// Namespace moved off this node — its streak must not linger and later
	// re-advertise a node that no longer serves it.
	advanceStreaks(st, map[string]*NamespaceHealth{})

	st.mu.RLock()
	_, lingering := st.healthyStreak["anchat-test"]
	st.mu.RUnlock()
	if lingering {
		t.Error("streak survived the namespace leaving this node")
	}
}

func TestStreak_unhealthyNeverBecomesReady(t *testing.T) {
	st := &namespaceHealthState{}
	for i := 0; i < healthyProbesBeforeDNSReclaim*3; i++ {
		if got := advanceStreaks(st, unhealthy()); len(got) != 0 {
			t.Fatalf("an unhealthy namespace became ready: %v", got)
		}
	}
}
