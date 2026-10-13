package tx_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// FuzzClientTxDecode: Builder.Decode, which a wallet runs on a transaction it did not build, never
// panics on any bytes. A transaction it accepts is bound to the chain id and the account number it
// was checked against (it fails under another of either), carries at least one message and a
// positive norama fee, and re-encodes to the very bytes it was read from.
func FuzzClientTxDecode(f *testing.F) {
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		f.Fatal(err)
	}
	var doc struct {
		ChainID       string `json:"chain_id"`
		AccountNumber uint64 `json:"account_number"`
		Cases         []struct {
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
		f.Add(tx, doc.ChainID, doc.AccountNumber)
	}
	f.Add([]byte{}, "orama-1", uint64(0))
	b := newBuilder(f)
	cfg := oramaTxConfig(f)
	f.Fuzz(func(t *testing.T, tx []byte, chainID string, account uint64) {
		decoded, err := b.Decode(tx, chainID, account)
		if err != nil {
			return
		}
		if len(decoded.MsgTypeURLs) == 0 || decoded.Signer == "" || !decoded.Fee.IsAllPositive() {
			t.Fatalf("an accepted transaction without a message, a signer or a fee: %+v", decoded)
		}
		if _, err := b.Decode(tx, chainID+"x", account); err == nil {
			t.Fatal("the transaction also verifies under another chain id")
		}
		if _, err := b.Decode(tx, chainID, account+1); err == nil {
			t.Fatal("the transaction also verifies under another account number")
		}
		sdkTx, err := cfg.TxDecoder()(tx)
		if err != nil {
			t.Fatalf("Decode accepted what the SDK decoder refuses: %v", err)
		}
		if back, err := cfg.TxEncoder()(sdkTx); err != nil || !bytes.Equal(back, tx) {
			t.Fatalf("an accepted transaction does not re-encode to its bytes (%v)", err)
		}
	})
}
