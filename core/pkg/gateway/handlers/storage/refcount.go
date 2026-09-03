package storage

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// ClusterUnpinner is the IPFS unpin surface shared by storage, deployments,
// and namespace-delete (bugboard #157).
type ClusterUnpinner interface {
	Unpin(ctx context.Context, cid string) error
}

// CIDInUseByOtherNamespace reports whether any namespace OTHER than the
// given one still holds this CID — either as a live ipfs_content_ownership
// pin or as a deployment content/build CID. IPFS-Cluster dedups to one pin
// per CID, so the last remaining user is the only one that may Unpin.
func CIDInUseByOtherNamespace(ctx context.Context, db rqlite.Client, cid, namespace string) (bool, error) {
	if db == nil || cid == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result []map[string]interface{}
	q := `SELECT COUNT(*) as count FROM ipfs_content_ownership WHERE cid = ? AND namespace != ? AND is_pinned = 1`
	if err := db.Query(ctx, &result, q, cid, namespace); err != nil {
		return false, err
	}
	if len(result) > 0 && countFromRow(result[0]["count"]) > 0 {
		return true, nil
	}

	result = nil
	q = `SELECT COUNT(*) as count FROM deployments WHERE namespace != ? AND (content_cid = ? OR build_cid = ?)`
	if err := db.Query(ctx, &result, q, namespace, cid, cid); err != nil {
		return false, err
	}
	if len(result) == 0 {
		return false, nil
	}
	return countFromRow(result[0]["count"]) > 0, nil
}

// UnpinIfLastPinner removes the cluster pin only when no other namespace
// still references the CID. On a refcount error it leaves the pin (a leak
// is recoverable; deleting live shared data is not). Empty cid or nil
// client is a no-op.
func UnpinIfLastPinner(ctx context.Context, db rqlite.Client, ipfs ClusterUnpinner, cid, namespace string) error {
	if cid == "" || ipfs == nil {
		return nil
	}
	inUse, err := CIDInUseByOtherNamespace(ctx, db, cid, namespace)
	if err != nil || inUse {
		return nil
	}
	return ipfs.Unpin(ctx, cid)
}
