package vault

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/shamir"
)

// thresholdsForGuardianCount returns the read threshold K and write quorum W
// for a fleet of n guardians.
//
// At n==1 Shamir cannot run (Split requires k>=2 and n>=k). Eval stores the
// envelope as a local key on that disk: K=1, W=1. This is not
// information-theoretic secret sharing — one disk holds the ciphertext.
//
// At n>=2 the Shamir formulas are unchanged: K = max(2, floor(n/3)),
// W = min(n, max(K+1, ceil(2n/3))). Do not fold K=1 into AdaptiveThreshold.
func thresholdsForGuardianCount(n int) (k, w int) {
	if n == 1 {
		return 1, 1
	}
	return shamir.AdaptiveThreshold(n), shamir.WriteQuorum(n)
}

// splitEnvelope is Shamir split for n>=2. At n==1 it returns one share whose
// Y is the envelope itself (X=1) and persists threshold 1.
func splitEnvelope(envelope []byte, n int) (shares []shamir.Share, k, w int, err error) {
	k, w = thresholdsForGuardianCount(n)
	if n == 1 {
		out := make([]byte, len(envelope))
		copy(out, envelope)
		return []shamir.Share{{X: 1, Y: out}}, k, w, nil
	}
	shares, err = shamir.Split(envelope, n, k)
	if err != nil {
		return nil, 0, 0, err
	}
	return shares, k, w, nil
}

// combineEnvelope reconstructs the envelope. Stored threshold 1 is a local
// key: return Y, do not call Combine (which refuses fewer than 2 shares).
// This stays keyed on the stored K so a 1→3 fleet growth does not brick
// envelopes written on one guardian.
func combineEnvelope(shares []shamir.Share, k int) ([]byte, error) {
	if k == 1 {
		if len(shares) < 1 {
			return nil, fmt.Errorf("local-key envelope is missing")
		}
		out := make([]byte, len(shares[0].Y))
		copy(out, shares[0].Y)
		return out, nil
	}
	if k > len(shares) {
		return nil, fmt.Errorf("need %d shares, have %d", k, len(shares))
	}
	return shamir.Combine(shares[:k])
}
