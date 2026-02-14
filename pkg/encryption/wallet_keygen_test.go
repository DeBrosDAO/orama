package encryption

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

// testMasterKey is a deterministic 32-byte key for testing ExpandNodeKeys.
// In production, this comes from `rw derive --salt "orama-node" --info "<IP>"`.
var testMasterKey = bytes.Repeat([]byte{0xab}, 32)
var testMasterKey2 = bytes.Repeat([]byte{0xcd}, 32)

func TestExpandNodeKeys_Determinism(t *testing.T) {
	keys1, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys: %v", err)
	}
	keys2, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys (second): %v", err)
	}

	if !bytes.Equal(keys1.LibP2PPrivateKey, keys2.LibP2PPrivateKey) {
		t.Error("LibP2P private keys differ for same input")
	}
	if !bytes.Equal(keys1.WireGuardKey[:], keys2.WireGuardKey[:]) {
		t.Error("WireGuard keys differ for same input")
	}
	if !bytes.Equal(keys1.IPFSPrivateKey, keys2.IPFSPrivateKey) {
		t.Error("IPFS private keys differ for same input")
	}
	if !bytes.Equal(keys1.ClusterPrivateKey, keys2.ClusterPrivateKey) {
		t.Error("Cluster private keys differ for same input")
	}
	if !bytes.Equal(keys1.JWTPrivateKey, keys2.JWTPrivateKey) {
		t.Error("JWT private keys differ for same input")
	}
}

func TestExpandNodeKeys_Uniqueness(t *testing.T) {
	keys1, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys(master1): %v", err)
	}
	keys2, err := ExpandNodeKeys(testMasterKey2)
	if err != nil {
		t.Fatalf("ExpandNodeKeys(master2): %v", err)
	}

	if bytes.Equal(keys1.LibP2PPrivateKey, keys2.LibP2PPrivateKey) {
		t.Error("LibP2P keys should differ for different master keys")
	}
	if bytes.Equal(keys1.WireGuardKey[:], keys2.WireGuardKey[:]) {
		t.Error("WireGuard keys should differ for different master keys")
	}
	if bytes.Equal(keys1.IPFSPrivateKey, keys2.IPFSPrivateKey) {
		t.Error("IPFS keys should differ for different master keys")
	}
	if bytes.Equal(keys1.ClusterPrivateKey, keys2.ClusterPrivateKey) {
		t.Error("Cluster keys should differ for different master keys")
	}
	if bytes.Equal(keys1.JWTPrivateKey, keys2.JWTPrivateKey) {
		t.Error("JWT keys should differ for different master keys")
	}
}

func TestExpandNodeKeys_KeysAreMutuallyUnique(t *testing.T) {
	keys, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys: %v", err)
	}

	privKeys := [][]byte{
		keys.LibP2PPrivateKey.Seed(),
		keys.IPFSPrivateKey.Seed(),
		keys.ClusterPrivateKey.Seed(),
		keys.JWTPrivateKey.Seed(),
		keys.WireGuardKey[:],
	}
	labels := []string{"LibP2P", "IPFS", "Cluster", "JWT", "WireGuard"}

	for i := 0; i < len(privKeys); i++ {
		for j := i + 1; j < len(privKeys); j++ {
			if bytes.Equal(privKeys[i], privKeys[j]) {
				t.Errorf("%s and %s keys should differ", labels[i], labels[j])
			}
		}
	}
}

func TestExpandNodeKeys_Ed25519Validity(t *testing.T) {
	keys, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys: %v", err)
	}

	msg := []byte("test message for verification")

	pairs := []struct {
		name string
		priv ed25519.PrivateKey
		pub  ed25519.PublicKey
	}{
		{"LibP2P", keys.LibP2PPrivateKey, keys.LibP2PPublicKey},
		{"IPFS", keys.IPFSPrivateKey, keys.IPFSPublicKey},
		{"Cluster", keys.ClusterPrivateKey, keys.ClusterPublicKey},
		{"JWT", keys.JWTPrivateKey, keys.JWTPublicKey},
	}

	for _, p := range pairs {
		signature := ed25519.Sign(p.priv, msg)
		if !ed25519.Verify(p.pub, msg, signature) {
			t.Errorf("%s key pair: signature verification failed", p.name)
		}
	}
}

func TestExpandNodeKeys_WireGuardClamping(t *testing.T) {
	keys, err := ExpandNodeKeys(testMasterKey)
	if err != nil {
		t.Fatalf("ExpandNodeKeys: %v", err)
	}

	if keys.WireGuardKey[0]&7 != 0 {
		t.Errorf("WireGuard key not properly clamped: low 3 bits of first byte should be 0, got %08b", keys.WireGuardKey[0])
	}
	if keys.WireGuardKey[31]&128 != 0 {
		t.Errorf("WireGuard key not properly clamped: high bit of last byte should be 0, got %08b", keys.WireGuardKey[31])
	}
	if keys.WireGuardKey[31]&64 != 64 {
		t.Errorf("WireGuard key not properly clamped: second-high bit of last byte should be 1, got %08b", keys.WireGuardKey[31])
	}

	var zero [32]byte
	if keys.WireGuardPubKey == zero {
		t.Error("WireGuard public key is all zeros")
	}
}

func TestExpandNodeKeys_InvalidMasterKeyLength(t *testing.T) {
	_, err := ExpandNodeKeys(nil)
	if err == nil {
		t.Error("expected error for nil master key")
	}

	_, err = ExpandNodeKeys([]byte{})
	if err == nil {
		t.Error("expected error for empty master key")
	}

	_, err = ExpandNodeKeys(make([]byte, 16))
	if err == nil {
		t.Error("expected error for 16-byte master key")
	}

	_, err = ExpandNodeKeys(make([]byte, 64))
	if err == nil {
		t.Error("expected error for 64-byte master key")
	}
}

func TestHexToBytes(t *testing.T) {
	tests := []struct {
		input    string
		expected []byte
		wantErr  bool
	}{
		{"", []byte{}, false},
		{"00", []byte{0}, false},
		{"ff", []byte{255}, false},
		{"FF", []byte{255}, false},
		{"0a1b2c", []byte{10, 27, 44}, false},
		{"0", nil, true},      // odd length
		{"zz", nil, true},     // invalid chars
		{"gg", nil, true},     // invalid chars
	}

	for _, tt := range tests {
		got, err := hexToBytes(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("hexToBytes(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("hexToBytes(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if !bytes.Equal(got, tt.expected) {
			t.Errorf("hexToBytes(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestDeriveNodeKeysFromWallet_EmptyIP(t *testing.T) {
	_, err := DeriveNodeKeysFromWallet("")
	if err == nil {
		t.Error("expected error for empty VPS IP")
	}
}
