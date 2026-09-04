package install

import (
	"testing"
)

// Every node's gateway serves /v1/proxy/anon, which needs a local anon SOCKS5
// proxy on :9050. Install always enables Anyone client mode.

func TestNewOrchestrator_defaultsToAnyoneClient(t *testing.T) {
	o, err := NewOrchestrator(&Flags{})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if !o.setup.IsAnyoneClient() {
		t.Error("with no anyone flag, node should default to AnyoneClient=true (SOCKS proxy for /v1/proxy/anon)")
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
