package cluster

import "testing"

func TestShouldSkipServiceAlert_coreDNSInactiveOnAPlainNode(t *testing.T) {
	plain := &nodeContext{isNameserver: false}
	ns := &nodeContext{isNameserver: true}
	for _, name := range []string{"orama-namespace-coredns@nameserver", "coredns"} {
		if !shouldSkipServiceAlert(name, "inactive", plain) {
			t.Errorf("%s inactive on a plain node was alerted: it only runs on nameservers", name)
		}
		if shouldSkipServiceAlert(name, "inactive", ns) {
			t.Errorf("%s inactive on a nameserver was not alerted", name)
		}
		if shouldSkipServiceAlert(name, "failed", plain) {
			t.Errorf("a failed %s must always alert", name)
		}
	}
	if !shouldSkipServiceAlert("orama-namespace-coredns@nameserver", "inactive", nil) {
		t.Error("a node with no role context is not a nameserver")
	}
	if shouldSkipServiceAlert("orama-namespace-olric@index", "inactive", plain) {
		t.Error("only CoreDNS is nameserver-only")
	}
}
