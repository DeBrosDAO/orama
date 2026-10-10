package main

import (
	"strings"
	"testing"
)

// The index gateway bound every interface, the public one included. It binds
// the overlay, where other nodes reach it, and loopback, where Caddy and the
// CLI do.
func TestListenAddrs_indexGatewayBindsOverlayAndLoopback(t *testing.T) {
	got, err := listenAddrs("index", "10.0.0.7:10104")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "10.0.0.7:10104,127.0.0.1:10104" {
		t.Errorf("the index gateway binds %v", got)
	}
}

// A gateway whose client_namespace is still the pre-rename "default", or
// empty, is the index gateway. Without the loopback bind, Caddy and the CLI
// cannot reach it.
func TestListenAddrs_legacyDefaultBindsLoopback(t *testing.T) {
	for _, ns := range []string{"default", "", "  default  "} {
		got, err := listenAddrs(ns, "10.0.0.7:10104")
		if err != nil {
			t.Fatalf("listenAddrs(%q): %v", ns, err)
		}
		if strings.Join(got, ",") != "10.0.0.7:10104,127.0.0.1:10104" {
			t.Errorf("listenAddrs(%q) = %v", ns, got)
		}
	}
}

// A listen_addr from before this change is refused, never bound.
func TestListenAddrs_indexGatewayRefusesEveryInterface(t *testing.T) {
	for _, addr := range []string{":10104", "0.0.0.0:10104", "203.0.113.7:10104", "localhost:10104", "garbage"} {
		if got, err := listenAddrs("index", addr); err == nil {
			t.Errorf("listen_addr %q was bound as %v", addr, got)
		}
	}
}

// A tenant gateway binds exactly what the spawner wrote (its overlay address).
func TestListenAddrs_tenantGatewayBindsWhatItIsGiven(t *testing.T) {
	got, err := listenAddrs("alice", "10.0.0.7:10204")
	if err != nil || strings.Join(got, ",") != "10.0.0.7:10204" {
		t.Errorf("got %v, %v", got, err)
	}
}
