package indexer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// FuzzIndexerReadEnvelope: the explorer decodes every transaction it indexes from bytes a peer
// produced. For any bytes readEnvelope never panics and answers the same twice; an accepted one has
// one JSON body per message (each valid JSON, each carrying an @type for a message it could not
// read), and a signer that is empty or a valid address of the chain's prefix. msgResponses obeys
// the same rule on the bytes as a result.
func FuzzIndexerReadEnvelope(f *testing.F) {
	raw, err := os.ReadFile(filepath.Join("..", "client", "tx", "testdata", "tx_vectors.json"))
	if err != nil {
		f.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			TxHex string `json:"tx_hex"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		f.Fatal(err)
	}
	for _, c := range doc.Cases {
		tx, err := hex.DecodeString(c.TxHex)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(tx)
	}
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0x00, 0x12, 0x00})
	cdc := newCodec()
	f.Fuzz(func(t *testing.T, tx []byte) {
		env, err := readEnvelope(cdc, tx)
		if again, err2 := readEnvelope(cdc, bytes.Clone(tx)); (err == nil) != (err2 == nil) || (err == nil && !equalEnvelopes(env, again)) {
			t.Fatal("readEnvelope is not deterministic")
		}
		if err == nil {
			if len(env.body) != len(env.msgs) {
				t.Fatalf("%d message bodies for %d messages", len(env.body), len(env.msgs))
			}
			for i, js := range env.body {
				if !json.Valid(js) || !strings.Contains(string(js), "@type") {
					t.Fatalf("message %d body is not a typed JSON object: %s", i, js)
				}
			}
			if env.signer != "" {
				if prefix, _, err := bech32.DecodeAndConvert(env.signer); err != nil || prefix != params.Bech32Prefix {
					t.Fatalf("signer %q is not an address of this chain (%v)", env.signer, err)
				}
			}
		}
		if res, err := msgResponses(tx); err == nil {
			for _, r := range res {
				if r == nil {
					t.Fatal("a nil response in the result data")
				}
			}
		}
	})
}

func equalEnvelopes(a, b envelope) bool {
	if a.signer != b.signer || a.memo != b.memo || len(a.body) != len(b.body) {
		return false
	}
	for i := range a.body {
		if !bytes.Equal(a.body[i], b.body[i]) {
			return false
		}
	}
	return true
}
