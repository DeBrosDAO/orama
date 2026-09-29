package types

import (
	"context"
)

// NodesKeeper resolves an archiver. ArchiverOperator returns the operator of
// nodeID when that node is active with an ARCHIVER role bond and signer is
// its hot key, and an error otherwise.
type NodesKeeper interface {
	ArchiverOperator(ctx context.Context, nodeID, signer string) (string, error)
}

// StorageKeeper is x/archive's view of x/storage. ArchiveDealActive reports whether dealID is an
// ARCHIVE deal with at least one provider assigned. ArchiveDealLive also counts a deal that is
// still waiting for its first provider. CreateArchiveDeal opens a protocol ARCHIVE deal over a
// bundle with the given piece commitment and returns its id.
type StorageKeeper interface {
	ArchiveDealActive(ctx context.Context, dealID uint64) (bool, error)
	ArchiveDealLive(ctx context.Context, dealID uint64) (bool, error)
	CreateArchiveDeal(ctx context.Context, pieceRoot []byte, realLeafCount, paddedLeafCount, pieceBytes, durationEpochs uint64) (uint64, error)
}
