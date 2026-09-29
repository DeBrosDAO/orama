package orchard

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Bundles built by the F7 wallet crate (chain/x/shielded/wallet): real spends, real proofs.
const walletScenarioFile = "../../wallet/testdata/bundles/scenario.json"

type walletStep struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Bundle        string   `json:"bundle"`
	EffectingData string   `json:"effecting_data"`
	Sighash       string   `json:"sighash"`
	Anchor        string   `json:"anchor"`
	ValueBalance  int64    `json:"value_balance"`
	Nullifiers    []string `json:"nullifiers"`
	RealSpends    int      `json:"real_spends"`
}

type walletScenario struct {
	ChainID string       `json:"chain_id"`
	Steps   []walletStep `json:"steps"`
}

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}

func loadWalletScenario(t testing.TB) walletScenario {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(walletScenarioFile))
	if err != nil {
		t.Fatalf("read wallet scenario: %v", err)
	}
	var s walletScenario
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse wallet scenario: %v", err)
	}
	if len(s.Steps) == 0 {
		t.Fatal("wallet scenario has no steps")
	}
	return s
}

// The wallet computes the Orama sighash in Rust and the chain computes it in Go. They must agree
// on every bundle, including the ones with real spends.
func TestSighash_matchesWalletBuilder(t *testing.T) {
	s := loadWalletScenario(t)
	for _, step := range s.Steps {
		bundle := mustHex(t, step.Bundle)
		got, err := Sighash(s.ChainID, bundle)
		if err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
		if !bytes.Equal(got[:], mustHex(t, step.Sighash)) {
			t.Errorf("%s: Go sighash %x, wallet wrote %s", step.Name, got, step.Sighash)
		}
		if !bytes.HasPrefix(bundle, mustHex(t, step.EffectingData)) {
			t.Errorf("%s: wallet effecting data is not the bundle prefix", step.Name)
		}
		eff, err := effectingData(bundle)
		if err != nil || !bytes.Equal(eff, mustHex(t, step.EffectingData)) {
			t.Errorf("%s: Go effecting data differs from the wallet's (%v)", step.Name, err)
		}
	}
}

func TestWalletScenario_shape(t *testing.T) {
	s := loadWalletScenario(t)
	realSpends := 0
	for _, step := range s.Steps {
		realSpends += step.RealSpends
		bundle := mustHex(t, step.Bundle)
		n := int(bundle[0])
		if len(step.Nullifiers) != n {
			t.Errorf("%s: %d nullifiers for %d actions", step.Name, len(step.Nullifiers), n)
		}
		anchorAt := 1 + n*actionLen + 1 + 8
		if !bytes.Equal(bundle[anchorAt:anchorAt+32], mustHex(t, step.Anchor)) {
			t.Errorf("%s: anchor field differs from the bundle bytes", step.Name)
		}
	}
	if realSpends < 2 {
		t.Fatalf("scenario has %d real spends, want a transfer and an unshield", realSpends)
	}
}
