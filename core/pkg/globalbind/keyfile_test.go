package globalbind

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestReadKeyFile_cometEd25519UsesTheSeed(t *testing.T) {
	seed := bytesRepeat(0x42, 32)
	pub := bytesRepeat(0x07, 32)
	raw := append(append([]byte{}, seed...), pub...)
	body := `{"priv_key":{"type":"tendermint/PrivKeyEd25519","value":"` + base64.StdEncoding.EncodeToString(raw) + `"}}`
	path := filepath.Join(t.TempDir(), "priv_validator_key.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ReadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if key.Kind != KeyTypeEd25519 || len(key.Secret) != 32 || key.Secret[0] != 0x42 {
		t.Fatalf("key %+v", key)
	}
	if _, err := SignKey(key, "orama-stagenet-2", "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", "chain"); err != nil {
		t.Fatal(err)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
