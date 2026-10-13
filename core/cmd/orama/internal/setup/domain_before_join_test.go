package setup

import (
	"errors"
	"strings"
	"testing"
)

// The live stagenet create run of 2026-10-10: the second machine's invite could not
// be minted, because the first served no certificate for stagenet.orama.network,
// which nobody had delegated yet; setup printed the records only after every
// machine was in. The records come first now, and the joins wait for them.
func TestRun_domainIsDelegatedBeforeTheFirstJoin(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	created, wait, invite := h.w.index("cluster "+ip1), h.w.index("domain wait via "+ip1), h.w.index("invite on "+ip1)
	if created < 0 || wait < 0 || invite < 0 {
		t.Fatalf("missing steps:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if !(created < wait && wait < invite) {
		t.Errorf("want the first machine installed, then the domain waited for, then the invite minted:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if records := h.w.index("domain records via " + ip1); records < 0 || records > wait {
		t.Errorf("the records are printed before the wait:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_domainNotDelegatedStopsBeforeTheJoinWithAResume(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w, waitErr: errors.New("gave up waiting for NS records")}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	_, err := run(t, h, opts)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{ip2 + " cannot join", "gave up waiting for NS records", "--ip " + ip2, "resumes at this step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if h.w.index("invite on ") >= 0 || h.w.index("cluster "+ip2) >= 0 {
		t.Errorf("no invite is minted and nothing joins before the domain is delegated:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_domainIsWaitedForOnceBeforeTheJoins(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w}
	opts := h.opts(ip1, ip2, ip3)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	waits := 0
	for _, e := range h.w.entries() {
		if strings.HasPrefix(e, "domain wait") {
			waits++
		}
	}
	// One before the first join, one at the end for the nameservers the joins added.
	if waits != 2 {
		t.Errorf("domain waited for %d times, want 2:\n%s", waits, strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_noDomainNeverWaitsBeforeJoins(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w}
	opts := h.opts(ip1, ip2)
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	if h.w.index("domain ") >= 0 {
		t.Errorf("a run without --domain touches no domain:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

// The stagenet chain-8 create printed the records when four of its five machines had their
// nameserver slots; the fifth claimed its own a moment later. The final records wait for every
// machine of the run.
func TestRun_finalRecordsWaitForEveryMachinesNameserverSlot(t *testing.T) {
	h := newHarness()
	calls := 0
	h.deps.Domain = fakeDomain{w: h.w, slots: func() []string {
		calls++
		if calls < 3 {
			return []string{ip1, ip2}
		}
		return []string{ip1, ip2, ip3}
	}}
	opts := h.opts(ip1, ip2, ip3)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	if calls < 3 {
		t.Fatalf("the slots were read %d times; the run printed the records before the third machine had its slot", calls)
	}
	if strings.Contains(h.report.text(), "not every machine has its nameserver slot") {
		t.Errorf("the run gave up waiting although every slot came:\n%s", h.report.text())
	}
}

func TestRun_finalRecordsArePrintedWhenTheSlotsNeverCome(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w, slots: func() []string { return []string{ip1} }}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	if !strings.Contains(h.report.text(), "not every machine has its nameserver slot yet") {
		t.Errorf("a run whose slots never came must say so:\n%s", h.report.text())
	}
	if !strings.Contains(h.report.text(), "Create these records") {
		t.Error("the records the cluster has are still printed")
	}
}

// A slot read that keeps failing is named in the message, not only the deadline.
func TestRun_finalRecordsWaitNamesWhyTheSlotsCouldNotBeRead(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w, slotsErr: errors.New("the cluster's nameservers: connection refused")}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	if !strings.Contains(h.report.text(), "connection refused") {
		t.Errorf("the wait gave up without saying why the slots could not be read:\n%s", h.report.text())
	}
}

func TestAllNameservers(t *testing.T) {
	for _, tc := range []struct {
		want, have []string
		ok         bool
	}{
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a", "b"}, []string{"a"}, false},
		{nil, nil, true},
		{[]string{"z"}, make([]string, nameserverSlots), true},
	} {
		if got := allNameservers(tc.want, tc.have); got != tc.ok {
			t.Errorf("allNameservers(%v, %d have) = %v, want %v", tc.want, len(tc.have), got, tc.ok)
		}
	}
}
