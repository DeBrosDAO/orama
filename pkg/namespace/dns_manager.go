package namespace

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DNSRecordManager manages DNS records for namespace clusters.
// It creates and deletes DNS A records for namespace gateway endpoints.
type DNSRecordManager struct {
	db         rqlite.Client
	baseDomain string
	logger     *zap.Logger
}

// NewDNSRecordManager creates a new DNS record manager
func NewDNSRecordManager(db rqlite.Client, baseDomain string, logger *zap.Logger) *DNSRecordManager {
	return &DNSRecordManager{
		db:         db,
		baseDomain: baseDomain,
		logger:     logger.With(zap.String("component", "dns-record-manager")),
	}
}

// CreateNamespaceRecords creates DNS A records for a namespace cluster.
// Each namespace gets records for ns-{namespace}.{baseDomain} pointing to its gateway nodes.
// Multiple A records enable round-robin DNS load balancing.
func (drm *DNSRecordManager) CreateNamespaceRecords(ctx context.Context, namespaceName string, nodeIPs []string) error {
	internalCtx := client.WithInternalAuth(ctx)

	if len(nodeIPs) == 0 {
		return &ClusterError{Message: "no node IPs provided for DNS records"}
	}

	// FQDN for namespace gateway: ns-{namespace}.{baseDomain}.
	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Creating namespace DNS records",
		zap.String("namespace", namespaceName),
		zap.String("fqdn", fqdn),
		zap.Strings("node_ips", nodeIPs),
	)

	// First, delete any existing records for this namespace
	deleteQuery := `DELETE FROM dns_records WHERE fqdn = ? AND namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, fqdn, "namespace:"+namespaceName)
	if err != nil {
		drm.logger.Warn("Failed to delete existing DNS records", zap.Error(err))
		// Continue anyway - the insert will just add more records
	}

	// Create A records for each node IP
	for _, ip := range nodeIPs {
		recordID := uuid.New().String()
		insertQuery := `
			INSERT INTO dns_records (
				id, fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		now := time.Now()
		_, err := drm.db.Exec(internalCtx, insertQuery,
			recordID,
			fqdn,
			"A",
			ip,
			60,                            // 60 second TTL for quick failover
			"namespace:"+namespaceName,    // Track ownership with namespace prefix
			"cluster-manager",             // Created by the cluster manager
			true,                          // Active
			now,
			now,
		)
		if err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("failed to create DNS record for %s -> %s", fqdn, ip),
				Cause:   err,
			}
		}
	}

	// Also create wildcard records for deployments under this namespace
	// *.ns-{namespace}.{baseDomain} -> same IPs
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	// Delete existing wildcard records
	_, _ = drm.db.Exec(internalCtx, deleteQuery, wildcardFqdn, "namespace:"+namespaceName)

	for _, ip := range nodeIPs {
		recordID := uuid.New().String()
		insertQuery := `
			INSERT INTO dns_records (
				id, fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		now := time.Now()
		_, err := drm.db.Exec(internalCtx, insertQuery,
			recordID,
			wildcardFqdn,
			"A",
			ip,
			60,
			"namespace:"+namespaceName,
			"cluster-manager",
			true,
			now,
			now,
		)
		if err != nil {
			drm.logger.Warn("Failed to create wildcard DNS record",
				zap.String("fqdn", wildcardFqdn),
				zap.String("ip", ip),
				zap.Error(err),
			)
			// Continue - wildcard is nice to have but not critical
		}
	}

	drm.logger.Info("Namespace DNS records created",
		zap.String("namespace", namespaceName),
		zap.Int("record_count", len(nodeIPs)*2), // A + wildcard
	)

	return nil
}

// DeleteNamespaceRecords deletes all DNS records for a namespace
func (drm *DNSRecordManager) DeleteNamespaceRecords(ctx context.Context, namespaceName string) error {
	internalCtx := client.WithInternalAuth(ctx)

	drm.logger.Info("Deleting namespace DNS records",
		zap.String("namespace", namespaceName),
	)

	// Delete all records owned by this namespace
	deleteQuery := `DELETE FROM dns_records WHERE namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, "namespace:"+namespaceName)
	if err != nil {
		return &ClusterError{
			Message: "failed to delete namespace DNS records",
			Cause:   err,
		}
	}

	drm.logger.Info("Namespace DNS records deleted",
		zap.String("namespace", namespaceName),
	)

	return nil
}

// GetNamespaceGatewayIPs returns the IP addresses for a namespace's gateway
func (drm *DNSRecordManager) GetNamespaceGatewayIPs(ctx context.Context, namespaceName string) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)

	type recordRow struct {
		Value string `db:"value"`
	}

	var records []recordRow
	query := `SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND is_active = TRUE`
	err := drm.db.Query(internalCtx, &records, query, fqdn)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query namespace DNS records",
			Cause:   err,
		}
	}

	ips := make([]string, len(records))
	for i, r := range records {
		ips[i] = r.Value
	}

	return ips, nil
}

// UpdateNamespaceRecord updates a specific node's DNS record (for failover)
func (drm *DNSRecordManager) UpdateNamespaceRecord(ctx context.Context, namespaceName, oldIP, newIP string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Updating namespace DNS record",
		zap.String("namespace", namespaceName),
		zap.String("old_ip", oldIP),
		zap.String("new_ip", newIP),
	)

	// Update both the main record and wildcard record
	for _, f := range []string{fqdn, wildcardFqdn} {
		updateQuery := `UPDATE dns_records SET value = ?, updated_at = ? WHERE fqdn = ? AND value = ?`
		_, err := drm.db.Exec(internalCtx, updateQuery, newIP, time.Now(), f, oldIP)
		if err != nil {
			drm.logger.Warn("Failed to update DNS record",
				zap.String("fqdn", f),
				zap.Error(err),
			)
		}
	}

	return nil
}

// DisableNamespaceRecord marks a specific IP's record as inactive (for temporary failover)
func (drm *DNSRecordManager) DisableNamespaceRecord(ctx context.Context, namespaceName, ip string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Disabling namespace DNS record",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
	)

	for _, f := range []string{fqdn, wildcardFqdn} {
		updateQuery := `UPDATE dns_records SET is_active = FALSE, updated_at = ? WHERE fqdn = ? AND value = ?`
		_, _ = drm.db.Exec(internalCtx, updateQuery, time.Now(), f, ip)
	}

	return nil
}

// EnableNamespaceRecord marks a specific IP's record as active (for recovery)
func (drm *DNSRecordManager) EnableNamespaceRecord(ctx context.Context, namespaceName, ip string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Enabling namespace DNS record",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
	)

	for _, f := range []string{fqdn, wildcardFqdn} {
		updateQuery := `UPDATE dns_records SET is_active = TRUE, updated_at = ? WHERE fqdn = ? AND value = ?`
		_, _ = drm.db.Exec(internalCtx, updateQuery, time.Now(), f, ip)
	}

	return nil
}
