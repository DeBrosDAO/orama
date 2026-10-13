package spikes_test

import (
	"bytes"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtstate "github.com/cometbft/cometbft/state"
	"github.com/stretchr/testify/require"
)

// CometBFT v0.39.4 hashes tx results into the next block's LastResultsHash.
// DeterministicExecTxResult keeps code, data, gas wanted and gas used, and
// drops events, log, info and codespace. A cNFT leaf that exists only as an
// event is therefore not committed.
func TestTxResultsHashIgnoresEvents(t *testing.T) {
	base := &abci.ExecTxResult{
		Code:      0,
		Data:      []byte("committed-leaf-bytes"),
		Log:       "indexer-only",
		Info:      "indexer-only",
		GasWanted: 100,
		GasUsed:   40,
		Codespace: "cnft",
	}
	withEvent := &abci.ExecTxResult{
		Code:      base.Code,
		Data:      append([]byte(nil), base.Data...),
		Log:       "different-log",
		Info:      "different-info",
		GasWanted: base.GasWanted,
		GasUsed:   base.GasUsed,
		Events: []abci.Event{{
			Type: "cnft.leaf",
			Attributes: []abci.EventAttribute{{
				Key:   "leaf",
				Value: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			}},
		}},
		Codespace: "other",
	}
	dataChanged := &abci.ExecTxResult{
		Code:      base.Code,
		Data:      []byte("other-leaf-bytes"),
		GasWanted: base.GasWanted,
		GasUsed:   base.GasUsed,
	}

	baseHash := cmtstate.TxResultsHash([]*abci.ExecTxResult{base})
	eventHash := cmtstate.TxResultsHash([]*abci.ExecTxResult{withEvent})
	dataHash := cmtstate.TxResultsHash([]*abci.ExecTxResult{dataChanged})

	require.Equal(t, baseHash, eventHash)
	require.False(t, bytes.Equal(baseHash, dataHash))
	require.Len(t, baseHash, 32)

	stripped := abci.DeterministicExecTxResult(withEvent)
	raw, err := stripped.Marshal()
	require.NoError(t, err)
	require.NotContains(t, raw, []byte("cnft.leaf"))
	require.NotContains(t, raw, []byte("ffffffffffffffff"))

	t.Logf("last_results_hash=%x events_change_hash=false data_changes_hash=true deterministic_bytes=%d",
		baseHash, len(raw))
}
