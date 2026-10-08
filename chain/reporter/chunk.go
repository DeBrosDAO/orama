package reporter

import (
	"fmt"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// DefaultChunkEntries is how many relays one MsgReportEpoch carries. A
// transaction of 1000 entries is about 150 KB, well inside CometBFT's default
// transaction size; the chain accepts up to MaxEntriesPerChunk.
const DefaultChunkEntries = 1000

// Messages splits entries into the chunks of one report. Every message carries
// the inputs_root of the whole report, which x/relay recomputes from the
// reassembled chunks. An empty report is one empty chunk. The same inputs give
// the same messages, so resubmitting after a crash is idempotent on chain.
func Messages(reporter string, epoch uint64, entries []relaytypes.RelayObservation, chunkSize int) ([]*relaytypes.MsgReportEpoch, error) {
	if chunkSize < 1 || chunkSize > relaytypes.MaxEntriesPerChunk {
		return nil, fmt.Errorf("chunk size %d is outside 1..%d", chunkSize, relaytypes.MaxEntriesPerChunk)
	}
	count := max((len(entries)+chunkSize-1)/chunkSize, 1)
	if uint32(count) > relaytypes.MaxChunkCount {
		return nil, fmt.Errorf("%d relays need %d chunks of %d, the chain accepts %d chunks", len(entries), count, chunkSize, relaytypes.MaxChunkCount)
	}
	root, err := relaytypes.InputsRoot(entries)
	if err != nil {
		return nil, fmt.Errorf("compute inputs_root: %w", err)
	}
	msgs := make([]*relaytypes.MsgReportEpoch, count)
	for i := range msgs {
		lo := min(i*chunkSize, len(entries))
		hi := min(lo+chunkSize, len(entries))
		msgs[i] = &relaytypes.MsgReportEpoch{
			Reporter:   reporter,
			Epoch:      epoch,
			ChunkIndex: uint32(i),
			ChunkCount: uint32(count),
			Entries:    entries[lo:hi],
			InputsRoot: root,
		}
	}
	return msgs, nil
}
