package install

import "testing"

// Every node's gateway serves /v1/proxy/anon, which needs a local anon SOCKS5
// proxy on :9050. A node installed with neither --anyone-relay nor
// --anyone-client previously had no SOCKS proxy, so /v1/proxy/anon returned
// "Anyone proxy not available at localhost:9050". Non-relay nodes must now
// default to Anyone client mode.

func TestNewOrchestrator_defaultsToAnyoneClient(t *testing.T) {
	o, err := NewOrchestrator(&Flags{})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if !o.setup.IsAnyoneClient() {
		t.Error("with no anyone flag, node should default to AnyoneClient=true (SOCKS proxy for /v1/proxy/anon)")
	}
}

func TestNewOrchestrator_relayIsNotClient(t *testing.T) {
	// A relay already exposes :9050 via its own anonrc; it must NOT also be
	// configured as a client (the two modes share one anon instance).
	o, err := NewOrchestrator(&Flags{AnyoneRelay: true})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if o.setup.IsAnyoneClient() {
		t.Error("relay node must not be configured as anyone-client")
	}
}

func TestNewOrchestrator_explicitClientStillWorks(t *testing.T) {
	o, err := NewOrchestrator(&Flags{AnyoneClient: true})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if !o.setup.IsAnyoneClient() {
		t.Error("explicit --anyone-client should configure client mode")
	}
}
