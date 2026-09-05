package enroll

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testCode = "a1b2c3d4e5f60718293a"

// Enrollment carries the cluster secret, the swarm key and the node's WireGuard
// configuration. It used to cross the network as plaintext JSON on the node's
// public IP.
func TestSealAndOpen_roundTrip(t *testing.T) {
	payload := []byte(`{"cluster_secret":"the-cluster-secret"}`)

	sealed, err := Seal(testCode, payload)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed, "the-cluster-secret") {
		t.Fatal("the cluster secret is readable in the sealed payload")
	}

	opened, err := Open(testCode, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(opened) != string(payload) {
		t.Errorf("round trip returned %q", opened)
	}
}

// The code is what the operator carried from the console. Anyone who does not
// have it cannot read the payload and cannot produce one.
func TestOpen_refusesTheWrongCode(t *testing.T) {
	sealed, err := Seal(testCode, []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := Open("a1b2c3d4e5f60718293b", sealed); err == nil {
		t.Fatal("a payload opened under a code that differs by one character")
	}
}

// GCM authenticates, so a payload edited in flight fails to open rather than
// yielding attacker-chosen configuration.
func TestOpen_refusesAnAlteredPayload(t *testing.T) {
	sealed, err := Seal(testCode, []byte(`{"cluster_secret":"real"}`))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	altered := base64.StdEncoding.EncodeToString(raw)

	if _, err := Open(testCode, altered); err == nil {
		t.Fatal("an altered payload opened")
	}
}

// Two seals of the same payload must differ, or a passive observer learns that
// two nodes were sent the same configuration.
func TestSeal_isNotDeterministic(t *testing.T) {
	first, err := Seal(testCode, []byte("same"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	second, err := Seal(testCode, []byte("same"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if first == second {
		t.Error("sealing is deterministic, so the nonce is being reused")
	}
}

func TestSeal_refusesAnEmptyCode(t *testing.T) {
	if _, err := Seal("   ", []byte("x")); err == nil {
		t.Fatal("a payload was sealed with no code, so anyone could open it")
	}
	if _, err := Open("", "anything"); err == nil {
		t.Fatal("a payload was opened with no code")
	}
}

func TestOpen_refusesMalformedInput(t *testing.T) {
	for _, sealed := range []string{"", "not base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := Open(testCode, sealed); err == nil {
			t.Errorf("%q opened", sealed)
		}
	}
}

// The code is read off a console by a person and is the only thing standing
// between a booting node and somebody else's cluster. Four bytes was 32 bits.
func TestGenerateCode_isLongEnoughToBeAKey(t *testing.T) {
	code, err := generateCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(code) != codeBytes*2 {
		t.Errorf("code %q is %d characters, want %d", code, len(code), codeBytes*2)
	}
	if codeBytes < 10 {
		t.Errorf("a %d-byte registration code is %d bits: it keys the payload that "+
			"carries the cluster secret", codeBytes, codeBytes*8)
	}

	other, err := generateCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if other == code {
		t.Error("two codes came back identical")
	}
}

func TestGenerateAgentToken(t *testing.T) {
	token, err := generateAgentToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token %q is %d characters, want 64 (32 bytes)", token, len(token))
	}
	other, err := generateAgentToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if other == token {
		t.Error("two tokens came back identical")
	}
}
