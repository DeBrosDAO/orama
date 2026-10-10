package reporter

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func entriesOf(n int) []relaytypes.RelayObservation {
	out := make([]relaytypes.RelayObservation, n)
	for i := range out {
		f := fp(byte(i % 251))
		f[0], f[1] = byte(i>>8), byte(i)
		out[i] = Observation{Fingerprint: f, Ed25519: ed(byte(i % 200)), Weight: uint64(i + 1), Uptime: math.LegacyOneDec()}.Entry()
	}
	return out
}

func TestMessages_chunksReassembleToTheRoot(t *testing.T) {
	entries := entriesOf(2500)
	msgs, err := Messages("reporter", 7, entries, 1000)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	root, err := relaytypes.InputsRoot(entries)
	require.NoError(t, err)
	var joined []relaytypes.RelayObservation
	for i, m := range msgs {
		require.EqualValues(t, i, m.ChunkIndex)
		require.EqualValues(t, 3, m.ChunkCount)
		require.EqualValues(t, 7, m.Epoch)
		require.Equal(t, root, m.InputsRoot, "every chunk carries the root of the whole report")
		joined = append(joined, m.Entries...)
	}
	require.Len(t, joined, 2500)
	again, err := relaytypes.InputsRoot(joined)
	require.NoError(t, err)
	require.Equal(t, root, again, "the chain recomputes the same root from the reassembled chunks")
}

func TestMessages_deterministic(t *testing.T) {
	a, err := Messages("r", 1, entriesOf(1500), 1000)
	require.NoError(t, err)
	b, err := Messages("r", 1, entriesOf(1500), 1000)
	require.NoError(t, err)
	require.Equal(t, a, b, "a resubmission after a crash is byte-identical, which x/relay takes idempotently")
}

func TestMessages_emptyReportIsOneEmptyChunk(t *testing.T) {
	msgs, err := Messages("r", 1, nil, 1000)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].Entries)
	require.Len(t, msgs[0].InputsRoot, relaytypes.InputsRootLen)
}

func TestMessages_boundaries(t *testing.T) {
	msgs, err := Messages("r", 1, entriesOf(1000), 1000)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "an exact fit is one chunk")

	_, err = Messages("r", 1, nil, 0)
	require.Error(t, err)
	_, err = Messages("r", 1, nil, relaytypes.MaxEntriesPerChunk+1)
	require.Error(t, err)
	_, err = Messages("r", 1, entriesOf(5), 1)
	require.NoError(t, err)

	over := make([]relaytypes.RelayObservation, int(relaytypes.MaxChunkCount)+1)
	for i := range over {
		over[i] = entriesOf(1)[0]
	}
	_, err = Messages("r", 1, over, 1)
	require.Error(t, err, "more chunks than the chain accepts")
}
