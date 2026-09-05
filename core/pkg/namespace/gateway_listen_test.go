package namespace

import (
	"fmt"
	"strings"
	"testing"
)

// A tenant's gateway binds the overlay, not every interface. It used to bind
// `:port`, so the only thing between it and the internet was a firewall rule —
// and a rule is a thing that can be wrong, where a listener that is not on a
// public interface cannot be reached from one however the rules are written.

func withOverlayIP(t *testing.T, ip string, err error) {
	t.Helper()
	previous := overlayIP
	overlayIP = func() (string, error) { return ip, err }
	t.Cleanup(func() { overlayIP = previous })
}

func TestGatewayListenAddr_aTenantGatewayBindsTheOverlay(t *testing.T) {
	withOverlayIP(t, "10.0.0.7", nil)

	got, err := gatewayListenAddr("acme", 10104)
	if err != nil {
		t.Fatalf("gatewayListenAddr: %v", err)
	}
	if got != "10.0.0.7:10104" {
		t.Errorf("listen addr = %q, want the overlay address", got)
	}
	if strings.HasPrefix(got, ":") {
		t.Error("the gateway is bound to every interface")
	}
}

// Caddy reverse-proxies to localhost and the ACME internal endpoint is reached
// there, so the cluster's own gateway keeps binding everything.
func TestGatewayListenAddr_theIndexGatewayKeepsBindingEverything(t *testing.T) {
	withOverlayIP(t, "10.0.0.7", nil)

	for _, ns := range []string{"", "index", "default"} {
		got, err := gatewayListenAddr(ns, 10104)
		if err != nil {
			t.Fatalf("gatewayListenAddr(%q): %v", ns, err)
		}
		if got != ":10104" {
			t.Errorf("gatewayListenAddr(%q) = %q, want every interface", ns, got)
		}
	}
}

// A node whose WireGuard is not up cannot serve a tenant's gateway on the
// overlay. Falling back to every interface would silently put it on the public
// one, which is the thing being removed.
func TestGatewayListenAddr_refusesWhenTheOverlayIsNotUp(t *testing.T) {
	withOverlayIP(t, "", fmt.Errorf("wg0 interface not found"))

	if _, err := gatewayListenAddr("acme", 10104); err == nil {
		t.Fatal("a tenant gateway was configured with no overlay address")
	} else if !strings.Contains(err.Error(), "WireGuard") {
		t.Errorf("the error does not name what is missing: %v", err)
	}
}

// Bind and probe are one change: a health check on localhost reads every
// healthy gateway as dead once the gateway is on the overlay.
func TestNamespaceGatewayHealthURL_followsTheBind(t *testing.T) {
	withOverlayIP(t, "10.0.0.7", nil)

	if got := namespaceGatewayHealthURL("acme", 10104); got != "http://10.0.0.7:10104/v1/health" {
		t.Errorf("tenant probe = %q, want the overlay address", got)
	}
	if got := namespaceGatewayHealthURL("index", 10104); got != "http://localhost:10104/v1/health" {
		t.Errorf("index probe = %q, want localhost", got)
	}
}
