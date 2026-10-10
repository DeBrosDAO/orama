package storage

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// ClusterUnpinner is the IPFS unpin surface shared by storage, deployments,
// and namespace-delete (bugboard #157).
type ClusterUnpinner interface {
	Unpin(ctx context.Context, cid string) error
}

// ClusterPinner is what removing a pin needs: Unpin, and the means to put the
// pin back if a reference appeared while it was being removed
// (UnpinUnreferenced). ipfs.IPFSClient satisfies it.
type ClusterPinner interface {
	ClusterUnpinner
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error)
	PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error)
}
