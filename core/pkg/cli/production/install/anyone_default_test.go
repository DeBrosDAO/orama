package install

import (
	"strings"
	"testing"
)

// Every node's gateway serves /v1/proxy/anon, which needs a local anon SOCKS5
// proxy on :9050. Install always enables Anyone client mode.

func TestParseFlags_anyoneRelayRejected(t *testing.T) {
	_, err := ParseFlags([]string{"--anyone-relay"})
	if err == nil {
		t.Fatal("expected error for removed --anyone-relay flag")
	}
	if !strings.Contains(err.Error(), "anyone-relay") {
		t.Errorf("error should mention anyone-relay, got: %v", err)
	}
}

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
