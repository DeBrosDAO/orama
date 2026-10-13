package chainread

import (
	"encoding/base64"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/txgate"
)

// The gateway's transaction cap is the chain's own mempool limit, not a shielded figure: any
// transaction goes through this route. The largest shielded transaction the chain accepts (the
// protocol's ceiling bundle of 32 actions, 103,823 bytes, in chain/x/shielded/bundle, and the
// envelope around it) is a tenth of the cap, and the onion gate for private broadcasts takes a
// request no larger than the biggest of those in base64 and JSON. The relations are pinned here so
// a change to any one of the three caps shows what it breaks.
func TestTxCaps_relateToTheLargestShieldedTransaction(t *testing.T) {
	const largestShieldedTx = 103_823 + 4<<10
	if txMaxBytes != 1<<20 {
		t.Errorf("txMaxBytes %d is not CometBFT's 1 MiB mempool limit", txMaxBytes)
	}
	if largestShieldedTx*8 > txMaxBytes {
		t.Errorf("the largest shielded transaction (%d) is not far under txMaxBytes %d", largestShieldedTx, txMaxBytes)
	}
	if txMaxBody < base64.StdEncoding.EncodedLen(txMaxBytes) {
		t.Errorf("txMaxBody %d cannot carry txMaxBytes %d in base64", txMaxBody, txMaxBytes)
	}
	if txgate.MaxBody < base64.StdEncoding.EncodedLen(largestShieldedTx) {
		t.Errorf("the onion gate (%d) cannot carry the largest shielded transaction in base64 (%d)", txgate.MaxBody, base64.StdEncoding.EncodedLen(largestShieldedTx))
	}
	if txgate.MaxBody >= txMaxBody {
		t.Errorf("the onion gate's cap %d is not under the gateway's body cap %d", txgate.MaxBody, txMaxBody)
	}
}
