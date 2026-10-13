package txgate

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These numbers are chain/x/shielded/bundle's: Len(32) for the protocol's ceiling of 32 actions per
// bundle, 103,823 bytes, and Len(16) for the default limit, 53,325. The chain's own tests pin the
// same literals (bundle.TestMaxBytes_isTheSizeOfAMaximalBundle), so a change to the layout fails
// there and sends someone here.
const (
	ceilingBundleBytes = 103_823
	defaultBundleBytes = 53_325
)

func TestMaxBody_isTheCeilingBundlesTransactionInJSON(t *testing.T) {
	if maxShieldedBundle != ceilingBundleBytes {
		t.Fatalf("maxShieldedBundle %d, the chain's ceiling bundle is %d", maxShieldedBundle, ceilingBundleBytes)
	}
	// The biggest request there can be: a transaction of the ceiling bundle and the whole envelope
	// allowance, as the broadcast JSON the gate reads.
	tx := make([]byte, ceilingBundleBytes+txEnvelopeAllowance)
	body, err := json.Marshal(map[string]string{"tx_bytes": base64.StdEncoding.EncodeToString(tx), "mode": "BROADCAST_MODE_SYNC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > MaxBody {
		t.Errorf("a request carrying the largest shielded transaction is %d bytes, over MaxBody %d", len(body), MaxBody)
	}
	// Close to it, not far above: the headroom is the JSON allowance and rounding, and the gate
	// stays well under the 1 MiB the chain REST API and the gateway take.
	if MaxBody > len(body)+jsonAllowance+4 {
		t.Errorf("MaxBody %d is more than the largest request %d plus its allowance", MaxBody, len(body))
	}
	if MaxBody >= 1<<20 {
		t.Errorf("MaxBody %d is not under the 1 MiB transaction limit of the chain", MaxBody)
	}
	// The default limit's transaction is a good deal smaller.
	small := base64.StdEncoding.EncodedLen(defaultBundleBytes + txEnvelopeAllowance)
	if small >= MaxBody {
		t.Errorf("a default-limit transaction in base64 (%d) is not under MaxBody %d", small, MaxBody)
	}
}

// A request exactly as large as the largest shielded transaction passes the body limit.
func TestGate_takesTheLargestShieldedTransaction(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	tx := make([]byte, ceilingBundleBytes+txEnvelopeAllowance)
	body := `{"tx_bytes":"` + base64.StdEncoding.EncodeToString(tx) + `","mode":"BROADCAST_MODE_SYNC"}`
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "application/json", body); rec.Code != http.StatusOK {
		t.Fatalf("the largest shielded transaction = %d", rec.Code)
	}
	over := `{"tx_bytes":"` + strings.Repeat("A", MaxBody) + `"}`
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "application/json", over); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body over MaxBody = %d", rec.Code)
	}
}
