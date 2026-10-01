package health

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// tokenRefreshInterval is how often every local deployment is given a fresh
// staged credential. A workload token lasts an hour and a unit reads the staged
// one whenever systemd starts it, with no gateway involved (a crash restart,
// a reboot), so the staged one must never be more than this old: a start
// always finds at least 40 minutes of life in it, and a gateway that is down
// for longer than that leaves the next unit start with an expired token.
const tokenRefreshInterval = 20 * time.Minute

const (
	// tokenRefreshSpacing paces a sweep: each refresh is registry reads and a
	// signature, and a node with hundreds of deployments should spread them out
	// rather than burst.
	tokenRefreshSpacing = 50 * time.Millisecond
	// tokenRefreshTimeout bounds one deployment's refresh, so one slow call
	// cannot hold up the rest of the sweep.
	tokenRefreshTimeout = 15 * time.Second
	// The first sweep retries a failed listing from tokenListRetryMin, doubling
	// to tokenListRetryMax, until it succeeds: a gateway that starts while the
	// registry is not yet answering still brings the staged tokens up to date.
	tokenListRetryMin = time.Second
	tokenListRetryMax = time.Minute
)

// runTokenRefresh refreshes at start, so a gateway that was down brings the
// staged tokens up to date at once (retrying the listing until it is read),
// and then every tokenRefreshInterval.
func (hc *HealthChecker) runTokenRefresh(ctx context.Context) {
	delay := tokenListRetryMin
	for {
		if _, _, err := hc.refreshTokens(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, tokenListRetryMax)
	}
	ticker := time.NewTicker(tokenRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := hc.refreshTokens(ctx); err != nil {
				hc.logger.Error("Could not list the deployments whose credential to refresh; retrying at the next sweep", zap.Error(err))
			}
		}
	}
}

// refreshTokens stages a fresh credential for every deployment with an active
// replica on this node. It reads the deployment rows, so a deployment deleted
// since is not refreshed, and a mint is refused for one deleted or stopped in
// between. A failure is logged for that deployment and does not stop the
// others; it returns how many were refreshed and how many failed, and an error
// only when the deployments could not be listed.
func (hc *HealthChecker) refreshTokens(ctx context.Context) (refreshed, failed int, err error) {
	var rows []deploymentRow
	query := `
		SELECT d.id, d.namespace, d.name, d.type, dr.port
		FROM deployments d
		JOIN deployment_replicas dr ON d.id = dr.deployment_id
		WHERE d.status IN ('active', 'degraded')
		  AND dr.node_id = ?
		  AND dr.status = 'active'
		  AND dr.port > 0
		  AND d.type IN ('nextjs', 'nodejs-backend', 'go-backend')
	`
	if err := hc.db.Query(ctx, &rows, query, hc.nodeID); err != nil {
		return 0, 0, fmt.Errorf("list the deployments on node %s: %w", hc.nodeID, err)
	}
	for i, row := range rows {
		if i > 0 && hc.refreshSpacing > 0 {
			select {
			case <-ctx.Done():
				return refreshed, failed, ctx.Err()
			case <-time.After(hc.refreshSpacing):
			}
		}
		d := &deployments.Deployment{
			ID:        row.ID,
			Namespace: row.Namespace,
			Name:      row.Name,
			Type:      deployments.DeploymentType(row.Type),
			Port:      row.Port,
		}
		callCtx, cancel := context.WithTimeout(ctx, tokenRefreshTimeout)
		err := hc.processManager.RefreshToken(callCtx, d)
		cancel()
		if err != nil {
			failed++
			level := hc.logger.Error
			if errors.Is(err, process.ErrDeploymentGone) || errors.Is(err, process.ErrStopped) {
				level = hc.logger.Info
			}
			level("Could not refresh a deployment's credential",
				zap.String("namespace", row.Namespace), zap.String("deployment", row.Name), zap.Error(err))
			continue
		}
		refreshed++
	}
	return refreshed, failed, nil
}
