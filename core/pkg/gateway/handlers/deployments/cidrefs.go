package deployments

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"go.uber.org/zap"
)

// A deployment holds its content and build CIDs in the cluster reference index
// (storage.CIDRefs) exactly as a storage pin does, so unpinning a CID one
// namespace no longer uses cannot remove the cluster pin another namespace's
// deployment or storage still holds. References are written here, when the
// gateway creates, updates, rolls back or deletes a deployment, and nowhere
// else: never derived from the deployments table, which a tenant with
// database access can write.

// SetCIDRefs gives the service the gateway's reference index. Only a service
// built by a test has none, and then references are neither recorded nor
// released.
func (s *DeploymentService) SetCIDRefs(refs *storage.CIDRefs) { s.cidRefs = refs }

// registerCIDs records that the deployment's namespace holds each CID and
// returns the CIDs it registered, for unregisterCIDs if the change they belong
// to does not happen. Registrations are counted by the index, so undoing one
// leaves a concurrent registration of the same content (another create with
// identical content, say) standing. Registration precedes the change, so
// another namespace unpinning the same bytes meanwhile already counts it.
func (s *DeploymentService) registerCIDs(ctx context.Context, namespace string, cids ...string) ([]string, error) {
	if s.cidRefs == nil {
		return nil, nil
	}
	var registered []string
	seen := make(map[string]struct{}, len(cids))
	for _, cid := range cids {
		if _, dup := seen[cid]; cid == "" || dup {
			continue
		}
		seen[cid] = struct{}{}
		if err := s.cidRefs.Register(ctx, cid, namespace, storage.KindDeployment); err != nil {
			s.unregisterCIDs(ctx, namespace, registered)
			return nil, fmt.Errorf("failed to register deployment content of namespace %s: %w", namespace, err)
		}
		registered = append(registered, cid)
	}
	return registered, nil
}

// unregisterCIDs undoes registerCIDs when the change did not happen. It runs on
// a context that outlives the request.
func (s *DeploymentService) unregisterCIDs(ctx context.Context, namespace string, registered []string) {
	for _, cid := range registered {
		if err := s.cidRefs.Unregister(context.WithoutCancel(ctx), cid, namespace, storage.KindDeployment); err != nil {
			s.logger.Error("Failed to take back the reference of a deployment change that did not happen; the CID stays referenced until the namespace's deployments stop using it",
				zap.String("namespace", namespace), zap.String("cid", cid), zap.Error(err))
		}
	}
}

// releaseCID drops the deployment's reference to cid and removes the cluster pin
// if nothing anywhere references it any more. Another deployment of the same
// namespace serving the CID keeps the reference: the index has one row per
// (cid, namespace, kind), which those deployments share. The namespace's own
// database is trusted for that count because a forged row there only makes the
// namespace keep its own reference.
func (s *DeploymentService) releaseCID(ctx context.Context, ipfs storage.ClusterPinner, deploymentID, namespace, cid string) error {
	if cid == "" || s.cidRefs == nil {
		return nil
	}
	var rows []map[string]interface{}
	if err := s.db.Query(ctx, &rows,
		`SELECT COUNT(*) AS count FROM deployments WHERE namespace = ? AND id != ? AND (content_cid = ? OR build_cid = ?)`,
		namespace, deploymentID, cid, cid); err != nil {
		return fmt.Errorf("failed to check whether another deployment of %s uses %s: %w", namespace, cid, err)
	}
	if len(rows) > 0 && rowCount(rows[0]["count"]) > 0 {
		return nil
	}
	return storage.UnpinIfLastRef(ctx, s.cidRefs, ipfs, cid, namespace, storage.KindDeployment)
}

// rowCount coerces a COUNT(*) cell (rqlite returns float64 or int64) to int.
func rowCount(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	}
	return 0
}
