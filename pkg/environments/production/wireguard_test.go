package production

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Keys should be base64, 44 chars (32 bytes + padding)
	if len(priv) != 44 {
		t.Errorf("private key length = %d, want 44", len(priv))
	}
	if len(pub) != 44 {
		t.Errorf("public key length = %d, want 44", len(pub))
	}

	// Should be valid base64
	if _, err := base64.StdEncoding.DecodeString(priv); err != nil {
		t.Errorf("private key is not valid base64: %v", err)
	}
	if _, err := base64.StdEncoding.DecodeString(pub); err != nil {
		t.Errorf("public key is not valid base64: %v", err)
	}

	// Private and public should differ
	if priv == pub {
		t.Error("private and public keys should differ")
	}
}

func TestGenerateKeyPair_Unique(t *testing.T) {
	priv1, _, _ := GenerateKeyPair()
	priv2, _, _ := GenerateKeyPair()

	if priv1 == priv2 {
		t.Error("two generated key pairs should be unique")
	}
}

func TestPublicKeyFromPrivate(t *testing.T) {
	priv, expectedPub, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	pub, err := PublicKeyFromPrivate(priv)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivate failed: %v", err)
	}

	if pub != expectedPub {
		t.Errorf("PublicKeyFromPrivate = %s, want %s", pub, expectedPub)
	}
}

func TestPublicKeyFromPrivate_InvalidKey(t *testing.T) {
	_, err := PublicKeyFromPrivate("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	_, err = PublicKeyFromPrivate(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestWireGuardProvisioner_GenerateConfig_NoPeers(t *testing.T) {
	wp := NewWireGuardProvisioner(WireGuardConfig{
		PrivateIP:  "10.0.0.1",
		ListenPort: 51820,
		PrivateKey: "dGVzdHByaXZhdGVrZXl0ZXN0cHJpdmF0ZWtleXM=",
	})

	config := wp.GenerateConfig()

	if !strings.Contains(config, "[Interface]") {
		t.Error("config should contain [Interface] section")
	}
	if !strings.Contains(config, "Address = 10.0.0.1/24") {
		t.Error("config should contain correct Address")
	}
	if !strings.Contains(config, "ListenPort = 51820") {
		t.Error("config should contain ListenPort")
	}
	if !strings.Contains(config, "PrivateKey = dGVzdHByaXZhdGVrZXl0ZXN0cHJpdmF0ZWtleXM=") {
		t.Error("config should contain PrivateKey")
	}
	if strings.Contains(config, "[Peer]") {
		t.Error("config should NOT contain [Peer] section with no peers")
	}
}

func TestWireGuardProvisioner_GenerateConfig_WithPeers(t *testing.T) {
	wp := NewWireGuardProvisioner(WireGuardConfig{
		PrivateIP:  "10.0.0.1",
		ListenPort: 51820,
		PrivateKey: "dGVzdHByaXZhdGVrZXl0ZXN0cHJpdmF0ZWtleXM=",
		Peers: []WireGuardPeer{
			{
				PublicKey: "cGVlcjFwdWJsaWNrZXlwZWVyMXB1YmxpY2tleXM=",
				Endpoint:  "1.2.3.4:51820",
				AllowedIP: "10.0.0.2/32",
			},
			{
				PublicKey: "cGVlcjJwdWJsaWNrZXlwZWVyMnB1YmxpY2tleXM=",
				Endpoint:  "5.6.7.8:51820",
				AllowedIP: "10.0.0.3/32",
			},
		},
	})

	config := wp.GenerateConfig()

	if strings.Count(config, "[Peer]") != 2 {
		t.Errorf("expected 2 [Peer] sections, got %d", strings.Count(config, "[Peer]"))
	}
	if !strings.Contains(config, "Endpoint = 1.2.3.4:51820") {
		t.Error("config should contain first peer endpoint")
	}
	if !strings.Contains(config, "AllowedIPs = 10.0.0.2/32") {
		t.Error("config should contain first peer AllowedIPs")
	}
	if !strings.Contains(config, "PersistentKeepalive = 25") {
		t.Error("config should contain PersistentKeepalive")
	}
	if !strings.Contains(config, "Endpoint = 5.6.7.8:51820") {
		t.Error("config should contain second peer endpoint")
	}
}

func TestWireGuardProvisioner_GenerateConfig_PeerWithoutEndpoint(t *testing.T) {
	wp := NewWireGuardProvisioner(WireGuardConfig{
		PrivateIP:  "10.0.0.1",
		ListenPort: 51820,
		PrivateKey: "dGVzdHByaXZhdGVrZXl0ZXN0cHJpdmF0ZWtleXM=",
		Peers: []WireGuardPeer{
			{
				PublicKey: "cGVlcjFwdWJsaWNrZXlwZWVyMXB1YmxpY2tleXM=",
				AllowedIP: "10.0.0.2/32",
			},
		},
	})

	config := wp.GenerateConfig()

	if strings.Contains(config, "Endpoint") {
		t.Error("config should NOT contain Endpoint when peer has none")
	}
}

func TestWireGuardProvisioner_DefaultPort(t *testing.T) {
	wp := NewWireGuardProvisioner(WireGuardConfig{
		PrivateIP:  "10.0.0.1",
		PrivateKey: "dGVzdHByaXZhdGVrZXl0ZXN0cHJpdmF0ZWtleXM=",
	})

	if wp.config.ListenPort != 51820 {
		t.Errorf("default ListenPort = %d, want 51820", wp.config.ListenPort)
	}
}
