package nsbackup

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

func TestRestoreKey_differs_per_namespace(t *testing.T) {
	root := secrets.Root{CurrentIKM: "root-a"}
	a, _, err := RestoreKey(root, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := RestoreKey(root, "other")
	if err != nil {
		t.Fatal(err)
	}
	if *a == *b {
		t.Fatal("two namespaces share a restore key")
	}
	if _, _, err := RestoreKey(root, "../x"); err == nil {
		t.Fatal("derived a key for an invalid namespace")
	}
}

// A request for another namespace cannot carry secrets sealed for this one,
// even when both are sealed to the same key: the namespace is inside the box.
func TestOpenSecrets_refuses_a_secret_replayed_into_another_namespace(t *testing.T) {
	root := secrets.Root{CurrentIKM: "root-a"}
	pub, priv, err := RestoreKey(root, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	req, err := testPayload().Rewrap(pub)
	if err != nil {
		t.Fatal(err)
	}
	moved := req
	moved.Namespace = "other"
	if _, err := moved.OpenSecrets(priv); !errors.Is(err, ErrSecretMismatch) {
		t.Fatalf("replayed namespace: %v", err)
	}
	swapped := req
	swapped.Secrets = append([]WrappedSecret(nil), req.Secrets...)
	swapped.Secrets[0].IDs = []string{"8"}
	if _, err := swapped.OpenSecrets(priv); !errors.Is(err, ErrSecretMismatch) {
		t.Fatalf("moved to another row: %v", err)
	}
}

func TestPayloadMarshal_refuses_more_than_MaxPins(t *testing.T) {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	pins := make([]string, 0, MaxPins+1)
	for i := 0; i <= MaxPins; i++ {
		suffix := []byte(strings.Repeat("1", 44))
		for n, pos := i, 43; n > 0; n, pos = n/len(alphabet), pos-1 {
			suffix[pos] = alphabet[n%len(alphabet)]
		}
		pins = append(pins, "Qm"+string(suffix))
	}
	p := Payload{Namespace: "myapp", RQLite: testDB(), Pins: pins[:MaxPins]}
	if _, err := p.Marshal(); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	p.Pins = pins
	if _, err := p.Marshal(); err == nil {
		t.Fatal("over the pin limit marshalled")
	}
}

func TestUnmarshalPayload_refuses_a_frame_over_MaxFrameBytes(t *testing.T) {
	big := make([]byte, MaxFrameBytes+1)
	copy(big, payloadMagic)
	if _, err := UnmarshalPayload(big); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversized frame: %v", err)
	}
}

func TestPayloadMarshal_refuses_negative_stored_bytes(t *testing.T) {
	p := testPayload()
	p.StoredBytes = -1
	if _, err := p.Marshal(); err == nil {
		t.Fatal("negative stored bytes marshalled")
	}
}
