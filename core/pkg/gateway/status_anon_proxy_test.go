package gateway

import "testing"

func TestAnonProxyCheck_reachableIsOK(t *testing.T) {
	got := anonProxyCheck(func() bool { return true })
	if got.Status != "ok" {
		t.Errorf("reachable Tor SOCKS port: status = %q, want ok", got.Status)
	}
	if got.Latency == "" {
		t.Error("an ok check must report its latency")
	}
}

// A stopped Tor client must not flip /v1/health to degraded: that status
// withdraws the node from DNS, and the node still serves everything but the
// anonymity proxy.
func TestAnonProxyCheck_unreachableIsUnavailableNotError(t *testing.T) {
	got := anonProxyCheck(func() bool { return false })
	if got.Status != "unavailable" {
		t.Errorf("unreachable Tor SOCKS port: status = %q, want unavailable", got.Status)
	}
	checks := map[string]checkResult{"rqlite": {Status: "ok"}, anonProxyCheckName: got}
	if s := aggregateHealthStatus(checks); s != "healthy" {
		t.Errorf("health with Tor down = %q, want healthy", s)
	}
}

// The key is part of the public /v1/health response.
func TestAnonProxyCheckName_isBackendNeutral(t *testing.T) {
	if anonProxyCheckName != "anon_proxy" {
		t.Errorf("anonProxyCheckName = %q; renaming it breaks /v1/health consumers", anonProxyCheckName)
	}
}
