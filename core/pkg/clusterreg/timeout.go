package clusterreg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// TimeoutHeightMargin is how many blocks past the newest one a transaction may still be included
// in. The chain refuses it after that, so a node that holds it cannot release it later. It is far
// longer than the two-minute wait for inclusion at any block time the networks run, and short
// enough that a lost transaction is dead within the hour.
const TimeoutHeightMargin = 200

// TimeoutHeightAfter is the timeout height of a transaction built when the newest block is latest.
func TimeoutHeightAfter(latest uint64) uint64 { return latest + TimeoutHeightMargin }

// FetchTimeoutHeight reads the newest block's height from the Cosmos REST API at base and returns
// the timeout height for a transaction built now.
func FetchTimeoutHeight(ctx context.Context, base string) (uint64, error) {
	latest, err := FetchLatestHeight(ctx, base)
	if err != nil {
		return 0, err
	}
	return TimeoutHeightAfter(latest), nil
}

// TxHash is the hash CometBFT and the Cosmos SDK give a transaction: the SHA-256 of its TxRaw
// bytes, in upper-case hex. A broadcast's answer must carry this hash; one that carries another
// is not about the transaction that was sent.
func TxHash(txRaw []byte) string {
	sum := sha256.Sum256(txRaw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
