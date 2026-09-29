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

// StorageKeeper reports whether dealID is an active x/storage ARCHIVE deal.
type StorageKeeper interface {
	ArchiveDealActive(ctx context.Context, dealID uint64) (bool, error)
}
