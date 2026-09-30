package storage

import "context"

// ClusterUnpinner is the IPFS unpin surface shared by storage, deployments,
// and namespace-delete (bugboard #157). Whether to call it is decided by
// UnpinIfLastRef, from the cluster-wide reference index (cidrefs.go).
type ClusterUnpinner interface {
	Unpin(ctx context.Context, cid string) error
}
