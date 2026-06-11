package main

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"gopkg.in/yaml.v3"
)

// TestDecodeTURNConfig_acceptsSpawnerOutput is the regression guard for the
// feat-124 crash: the namespace spawner writes the TURN config via
// yaml.Marshal(turn.Config), and the TURN binary parses it with a STRICT
// decoder. If turn.Config gains a yaml field the parser doesn't know, strict
// decode rejects it and TURN crash-loops at startup. This pins that the
// spawner's exact output round-trips through the parser, including the stealth
// fields.
func TestDecodeTURNConfig_acceptsSpawnerOutput(t *testing.T) {
	src := turn.Config{
		ListenAddr:         "0.0.0.0:3478",
		TURNSListenAddr:    "0.0.0.0:5349",
		PublicIP:           "203.0.113.7",
		Realm:              "orama-devnet.network",
		AuthSecret:         "secret",
		RelayPortStart:     49152,
		RelayPortEnd:       49951,
		Namespace:          "anchat-test",
		TLSCertPath:        "/x/turn-cert.pem",
		TLSKeyPath:         "/x/turn-key.pem",
		StealthDomain:      "cdn-3259254d4d3e.orama-devnet.network",
		TLSStealthCertPath: "/var/lib/caddy/caddy/certificates/.../wildcard_.orama-devnet.network.crt",
		TLSStealthKeyPath:  "/var/lib/caddy/caddy/certificates/.../wildcard_.orama-devnet.network.key",
	}

	data, err := yaml.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := decodeTURNConfig(data)
	if err != nil {
		t.Fatalf("strict decode of spawner output failed — TURN would crash-loop at startup: %v\n---\n%s", err, data)
	}

	if got.StealthDomain != src.StealthDomain ||
		got.TLSStealthCertPath != src.TLSStealthCertPath ||
		got.TLSStealthKeyPath != src.TLSStealthKeyPath {
		t.Errorf("stealth fields did not round-trip: got %+v", got)
	}
	if got.AuthSecret != src.AuthSecret || got.TURNSListenAddr != src.TURNSListenAddr {
		t.Errorf("core fields did not round-trip: got %+v", got)
	}
}

// TestDecodeTURNConfig_rejectsUnknownField confirms the strict decoder still
// rejects genuinely-unknown keys (so the contract above is meaningful).
func TestDecodeTURNConfig_rejectsUnknownField(t *testing.T) {
	if _, err := decodeTURNConfig([]byte("listen_addr: \"0.0.0.0:3478\"\nbogus_field: 1\n")); err == nil {
		t.Fatal("expected strict decode to reject an unknown field")
	}
}
