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
