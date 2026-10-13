//go:build e2e_fleet

package chain

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// blockIDFlagCommit is CometBFT's BlockIDFlagCommit: the signature is a
// precommit for the block.
const blockIDFlagCommit = 2

// CommitVotes is the time of block height and the time each validator stamped
// on its precommit for it, keyed by the validator's consensus address, as n
// stored the commit.
func (c *Chain) CommitVotes(t testing.TB, n fleet.Node, height int64) (blockTime time.Time, votes map[string]time.Time, err error) {
	t.Helper()
	var r struct {
		SignedHeader struct {
			Header struct {
				Time time.Time `json:"time"`
			} `json:"header"`
			Commit struct {
				Signatures []struct {
					BlockIDFlag      int       `json:"block_id_flag"`
					ValidatorAddress string    `json:"validator_address"`
					Timestamp        time.Time `json:"timestamp"`
				} `json:"signatures"`
			} `json:"commit"`
		} `json:"signed_header"`
	}
	if err := c.Comet(t, n, fmt.Sprintf("/commit?height=%d", height), &r); err != nil {
		return time.Time{}, nil, err
	}
	votes = map[string]time.Time{}
	for _, s := range r.SignedHeader.Commit.Signatures {
		if s.BlockIDFlag == blockIDFlagCommit {
			votes[s.ValidatorAddress] = s.Timestamp
		}
	}
	return r.SignedHeader.Header.Time, votes, nil
}
