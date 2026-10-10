package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// workloadIdentities is what the minter needs of the auth service.
type workloadIdentities interface {
	EnsureWorkloadPrincipal(ctx context.Context, namespace, name string) error
	MintWorkloadToken(ctx context.Context, namespace, name string) (string, time.Time, error)
}

// workloadTokenMinter issues the credential a deployment is started with, and
// only for a deployment whose row exists. A restart, a refresh or a start that
// raced the deployment's delete would otherwise record the principal and mint a
// token again for something that is gone, and the token would outlive it.
func workloadTokenMinter(ids workloadIdentities, registry rqlite.Client) process.WorkloadTokenMinter {
	return func(ctx context.Context, namespace, name string) (string, error) {
		var rows []struct {
			ID string `db:"id"`
		}
		if err := registry.Query(ctx, &rows,
			"SELECT id FROM deployments WHERE namespace = ? AND name = ? LIMIT 1", namespace, name); err != nil {
			return "", fmt.Errorf("check that deployment %s/%s exists: %w", namespace, name, err)
		}
		if len(rows) == 0 {
			return "", fmt.Errorf("%s/%s: %w", namespace, name, process.ErrDeploymentGone)
		}
		if err := ids.EnsureWorkloadPrincipal(ctx, namespace, name); err != nil {
			return "", err
		}
		token, _, err := ids.MintWorkloadToken(ctx, namespace, name)
		return token, err
	}
}

// workloadTokenRefresher is the mint behind a refresh of a running deployment.
// Its principal was recorded at Start, so there is no write to the registry
// here, and the health checker has just read the deployment's row, so there is
// no second read of it either: a sweep over hundreds of deployments costs
// reads, not Raft writes.
func workloadTokenRefresher(ids workloadIdentities) process.WorkloadTokenMinter {
	return func(ctx context.Context, namespace, name string) (string, error) {
		token, _, err := ids.MintWorkloadToken(ctx, namespace, name)
		return token, err
	}
}
