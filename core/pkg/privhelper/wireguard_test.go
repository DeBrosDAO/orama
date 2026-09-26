package privhelper

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// A real 32-byte key, base64.
const testKey = "q2h4Hw4mZb5J2qvZq8cG8c7Qm6dLhEjM0WZs5yY0yXE="

func TestValidate_WireGuardAddPeer(t *testing.T) {
	if _, err := Validate([]string{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "10.0.0.5/32"}); err != nil {
		t.Fatalf("a real join must be allowed: %v", err)
	}
	for _, argv := range [][]string{
		{"wireguard", "add-peer", "notakey", "203.0.113.7:51820", "10.0.0.5/32"},
		{"wireguard", "add-peer", testKey, "evil.example:51820", "10.0.0.5/32"},
		{"wireguard", "add-peer", testKey, "203.0.113.7", "10.0.0.5/32"},
		{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "0.0.0.0/0"},
		{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "10.0.0.0/24"},
		{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "192.168.1.5/32"},
		{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "10.0.0.5/32", "extra"},
		{"wireguard", "set-private-key", testKey},
		{"wireguard", "add-peer", testKey + "\nPostUp = id", "203.0.113.7:51820", "10.0.0.5/32"},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q must be refused", argv)
		}
	}
}

// The payload is data. Anything that could reach the conf as a directive —
// a newline in a key, a PostUp smuggled in a field — fails validation.
func TestParsePersistInput_ValidatesEveryPeer(t *testing.T) {
	good := []wireguard.Peer{{PublicKey: testKey, Endpoint: "203.0.113.7:51820", AllowedIP: "10.0.0.5/32"}, {PublicKey: strings.Replace(testKey, "q", "r", 1), AllowedIP: "10.0.0.6/32"}}
	data, _ := json.Marshal(good)
	got, err := ParsePersistInput(data)
	if err != nil || len(got) != 2 {
		t.Fatalf("valid peers refused: %v", err)
	}
	if got, err := ParsePersistInput([]byte("[]")); err != nil || len(got) != 0 {
		t.Errorf("an empty peer set is valid (a lone node): %v", err)
	}
	for _, bad := range []string{
		`[{"public_key":"x","allowed_ip":"10.0.0.5/32"}]`,
		`[{"public_key":"` + testKey + `","allowed_ip":"10.0.0.5/32\nPostUp = id"}]`,
		`[{"public_key":"` + testKey + `","endpoint":"a:1\nPostUp = id","allowed_ip":"10.0.0.5/32"}]`,
		`[{"public_key":"` + testKey + `","allowed_ip":"10.0.0.5/32"},{"public_key":"` + testKey + `","allowed_ip":"10.0.0.6/32"}]`,
		`not json`,
	} {
		if _, err := ParsePersistInput([]byte(bad)); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestInvocation_OnlyPersistPeersTakesInput(t *testing.T) {
	inv, _ := Validate([]string{"wireguard", "persist-peers"})
	if !inv.NeedsInput() {
		t.Error("persist-peers reads its peers as input")
	}
	inv, _ = Validate([]string{"systemctl", "daemon-reload"})
	if inv.NeedsInput() {
		t.Error("systemctl takes no input")
	}
	inv, err := Validate([]string{"gateway-key", "put", "jwt-signing-key.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if !inv.NeedsInput() {
		t.Fatal("gateway-key put must read the PEM; otherwise the stored key is empty")
	}
}

// Go's decoder skips '\r' and '\n': a key with a newline decoded to 32 bytes
// and landed in wg0.conf as two lines, breaking wg-quick at the next boot.
func TestValidatePeer_RefusesNonCanonicalKeys(t *testing.T) {
	withNewline := testKey[:20] + "\n" + testKey[20:]
	for _, key := range []string{withNewline, testKey + "\n", " " + testKey, strings.TrimSuffix(testKey, "=")} {
		if err := ValidatePeer(wireguard.Peer{PublicKey: key, AllowedIP: "10.0.0.5/32"}); err == nil {
			t.Errorf("key %q must be refused", key)
		}
	}
}

func TestValidate_WireGuardRemovePeerAndMeshBounds(t *testing.T) {
	if _, err := Validate([]string{"wireguard", "remove-peer", "10.0.0.7/32"}); err != nil {
		t.Fatalf("remove-peer of a mesh /32: %v", err)
	}
	for _, argv := range [][]string{
		{"wireguard", "remove-peer", "10.0.1.7/32"}, // outside the /24 mesh
		{"wireguard", "remove-peer", "10.0.0.7"},
		{"wireguard", "remove-peer", "10.0.0.07/32"},
		{"wireguard", "remove-peer", "10.0.0.0/24"},
		{"wireguard", "add-peer", testKey, "203.0.113.7:51820", "10.1.2.3/32"},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q must be refused", argv)
		}
	}
}

// Two peers claiming one /32 is a conf in which the second silently takes the
// first one's route when wg-quick applies it.
func TestParsePersistInput_RefusesDuplicateAllowedIP(t *testing.T) {
	other := strings.Replace(testKey, "q", "r", 1)
	dup := []wireguard.Peer{{PublicKey: testKey, AllowedIP: "10.0.0.5/32"}, {PublicKey: other, AllowedIP: "10.0.0.5/32"}}
	data, _ := json.Marshal(dup)
	_, err := ParsePersistInput(data)
	if err == nil || !strings.Contains(err.Error(), "10.0.0.5/32") {
		t.Fatalf("got %v, want the duplicate address named", err)
	}
	distinct := []wireguard.Peer{{PublicKey: testKey, AllowedIP: "10.0.0.5/32"}, {PublicKey: other, AllowedIP: "10.0.0.6/32"}}
	data, _ = json.Marshal(distinct)
	if _, err := ParsePersistInput(data); err != nil {
		t.Fatalf("distinct addresses refused: %v", err)
	}
}
