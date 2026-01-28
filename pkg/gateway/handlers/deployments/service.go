package deployments

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// subdomainSuffixLength is the length of the random suffix for deployment subdomains
	subdomainSuffixLength = 6
	// subdomainSuffixChars are the allowed characters for the random suffix (lowercase alphanumeric)
	subdomainSuffixChars = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// DeploymentService manages deployment operations
type DeploymentService struct {
	db              rqlite.Client
	homeNodeManager *deployments.HomeNodeManager
	portAllocator   *deployments.PortAllocator
	logger          *zap.Logger
	baseDomain      string // Base domain for deployments (e.g., "dbrs.space")
	nodePeerID      string // Current node's peer ID (deployments run on this node)
}

// NewDeploymentService creates a new deployment service
func NewDeploymentService(
	db rqlite.Client,
	homeNodeManager *deployments.HomeNodeManager,
	portAllocator *deployments.PortAllocator,
	logger *zap.Logger,
) *DeploymentService {
	return &DeploymentService{
		db:              db,
		homeNodeManager: homeNodeManager,
		portAllocator:   portAllocator,
		logger:          logger,
		baseDomain:      "dbrs.space", // default
	}
}

// SetBaseDomain sets the base domain for deployments
func (s *DeploymentService) SetBaseDomain(domain string) {
	if domain != "" {
		s.baseDomain = domain
	}
}

// SetNodePeerID sets the current node's peer ID
// Deployments will always run on this node (no cross-node routing for deployment creation)
func (s *DeploymentService) SetNodePeerID(peerID string) {
	s.nodePeerID = peerID
}

// BaseDomain returns the configured base domain
func (s *DeploymentService) BaseDomain() string {
	if s.baseDomain == "" {
		return "dbrs.space"
	}
	return s.baseDomain
}

// GetShortNodeID extracts a short node ID from a full peer ID for domain naming.
// e.g., "12D3KooWGqyuQR8N..." -> "node-GqyuQR"
// If the ID is already short (starts with "node-"), returns it as-is.
func GetShortNodeID(peerID string) string {
	// If already a short ID, return as-is
	if len(peerID) < 20 {
		return peerID
	}
	// Skip "12D3KooW" prefix (8 chars) and take next 6 chars
	if len(peerID) > 14 {
		return "node-" + peerID[8:14]
	}
	return "node-" + peerID[:6]
}

// generateRandomSuffix generates a random alphanumeric suffix for subdomains
func generateRandomSuffix(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based if crypto/rand fails
		return fmt.Sprintf("%06x", time.Now().UnixNano()%0xffffff)
	}
	for i := range b {
		b[i] = subdomainSuffixChars[int(b[i])%len(subdomainSuffixChars)]
	}
	return string(b)
}

// generateSubdomain generates a unique subdomain for a deployment
// Format: {name}-{random} (e.g., "myapp-f3o4if")
func (s *DeploymentService) generateSubdomain(ctx context.Context, name, namespace, deploymentID string) (string, error) {
	// Sanitize name for subdomain (lowercase, alphanumeric and hyphens only)
	sanitizedName := strings.ToLower(name)
	sanitizedName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, sanitizedName)
	// Remove consecutive hyphens and trim
	for strings.Contains(sanitizedName, "--") {
		sanitizedName = strings.ReplaceAll(sanitizedName, "--", "-")
	}
	sanitizedName = strings.Trim(sanitizedName, "-")

	// Try to generate a unique subdomain (max 10 attempts)
	for i := 0; i < 10; i++ {
		suffix := generateRandomSuffix(subdomainSuffixLength)
		subdomain := fmt.Sprintf("%s-%s", sanitizedName, suffix)

		// Check if subdomain is already taken globally
		exists, err := s.subdomainExists(ctx, subdomain)
		if err != nil {
			return "", fmt.Errorf("failed to check subdomain: %w", err)
		}
		if !exists {
			// Register the subdomain globally
			if err := s.registerSubdomain(ctx, subdomain, namespace, deploymentID); err != nil {
				// If registration fails (race condition), try again
				s.logger.Warn("Failed to register subdomain, retrying",
					zap.String("subdomain", subdomain),
					zap.Error(err),
				)
				continue
			}
			return subdomain, nil
		}
	}

	return "", fmt.Errorf("failed to generate unique subdomain after 10 attempts")
}

// subdomainExists checks if a subdomain is already registered globally
func (s *DeploymentService) subdomainExists(ctx context.Context, subdomain string) (bool, error) {
	type existsRow struct {
		Exists int `db:"exists"`
	}
	var rows []existsRow
	query := `SELECT 1 as exists FROM global_deployment_subdomains WHERE subdomain = ? LIMIT 1`
	err := s.db.Query(ctx, &rows, query, subdomain)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// registerSubdomain registers a subdomain in the global registry
func (s *DeploymentService) registerSubdomain(ctx context.Context, subdomain, namespace, deploymentID string) error {
	query := `
		INSERT INTO global_deployment_subdomains (subdomain, namespace, deployment_id, created_at)
		VALUES (?, ?, ?, ?)
	`
	_, err := s.db.Exec(ctx, query, subdomain, namespace, deploymentID, time.Now())
	return err
}

// CreateDeployment creates a new deployment
func (s *DeploymentService) CreateDeployment(ctx context.Context, deployment *deployments.Deployment) error {
	// Always use current node's peer ID for home node
	// Deployments run on the node that receives the creation request
	// This ensures port allocation matches where the service actually runs
	if s.nodePeerID != "" {
		deployment.HomeNodeID = s.nodePeerID
	} else if deployment.HomeNodeID == "" {
		// Fallback to home node manager if no node peer ID configured
		homeNodeID, err := s.homeNodeManager.AssignHomeNode(ctx, deployment.Namespace)
		if err != nil {
			return fmt.Errorf("failed to assign home node: %w", err)
		}
		deployment.HomeNodeID = homeNodeID
	}

	// Generate unique subdomain with random suffix if not already set
	// Format: {name}-{random} (e.g., "myapp-f3o4if")
	if deployment.Subdomain == "" {
		subdomain, err := s.generateSubdomain(ctx, deployment.Name, deployment.Namespace, deployment.ID)
		if err != nil {
			return fmt.Errorf("failed to generate subdomain: %w", err)
		}
		deployment.Subdomain = subdomain
	}

	// Allocate port for dynamic deployments
	if deployment.Type != deployments.DeploymentTypeStatic && deployment.Type != deployments.DeploymentTypeNextJSStatic {
		port, err := s.portAllocator.AllocatePort(ctx, deployment.HomeNodeID, deployment.ID)
		if err != nil {
			return fmt.Errorf("failed to allocate port: %w", err)
		}
		deployment.Port = port
	}

	// Serialize environment variables
	envJSON, err := json.Marshal(deployment.Environment)
	if err != nil {
		return fmt.Errorf("failed to marshal environment: %w", err)
	}

	// Insert deployment
	query := `
		INSERT INTO deployments (
			id, namespace, name, type, version, status,
			content_cid, build_cid, home_node_id, port, subdomain, environment,
			memory_limit_mb, cpu_limit_percent, disk_limit_mb,
			health_check_path, health_check_interval, restart_policy, max_restart_count,
			created_at, updated_at, deployed_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = s.db.Exec(ctx, query,
		deployment.ID, deployment.Namespace, deployment.Name, deployment.Type, deployment.Version, deployment.Status,
		deployment.ContentCID, deployment.BuildCID, deployment.HomeNodeID, deployment.Port, deployment.Subdomain, string(envJSON),
		deployment.MemoryLimitMB, deployment.CPULimitPercent, deployment.DiskLimitMB,
		deployment.HealthCheckPath, deployment.HealthCheckInterval, deployment.RestartPolicy, deployment.MaxRestartCount,
		deployment.CreatedAt, deployment.UpdatedAt, deployment.DeployedBy,
	)

	if err != nil {
		return fmt.Errorf("failed to insert deployment: %w", err)
	}

	// Record in history
	s.recordHistory(ctx, deployment, "deployed")

	s.logger.Info("Deployment created",
		zap.String("id", deployment.ID),
		zap.String("namespace", deployment.Namespace),
		zap.String("name", deployment.Name),
		zap.String("type", string(deployment.Type)),
		zap.String("home_node", deployment.HomeNodeID),
		zap.Int("port", deployment.Port),
	)

	return nil
}

// GetDeployment retrieves a deployment by namespace and name
func (s *DeploymentService) GetDeployment(ctx context.Context, namespace, name string) (*deployments.Deployment, error) {
	type deploymentRow struct {
		ID                  string    `db:"id"`
		Namespace           string    `db:"namespace"`
		Name                string    `db:"name"`
		Type                string    `db:"type"`
		Version             int       `db:"version"`
		Status              string    `db:"status"`
		ContentCID          string    `db:"content_cid"`
		BuildCID            string    `db:"build_cid"`
		HomeNodeID          string    `db:"home_node_id"`
		Port                int       `db:"port"`
		Subdomain           string    `db:"subdomain"`
		Environment         string    `db:"environment"`
		MemoryLimitMB       int       `db:"memory_limit_mb"`
		CPULimitPercent     int       `db:"cpu_limit_percent"`
		DiskLimitMB         int       `db:"disk_limit_mb"`
		HealthCheckPath     string    `db:"health_check_path"`
		HealthCheckInterval int       `db:"health_check_interval"`
		RestartPolicy       string    `db:"restart_policy"`
		MaxRestartCount     int       `db:"max_restart_count"`
		CreatedAt           time.Time `db:"created_at"`
		UpdatedAt           time.Time `db:"updated_at"`
		DeployedBy          string    `db:"deployed_by"`
	}

	var rows []deploymentRow
	query := `SELECT * FROM deployments WHERE namespace = ? AND name = ? LIMIT 1`
	err := s.db.Query(ctx, &rows, query, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment: %w", err)
	}

	if len(rows) == 0 {
		return nil, deployments.ErrDeploymentNotFound
	}

	row := rows[0]
	var env map[string]string
	if err := json.Unmarshal([]byte(row.Environment), &env); err != nil {
		env = make(map[string]string)
	}

	return &deployments.Deployment{
		ID:                  row.ID,
		Namespace:           row.Namespace,
		Name:                row.Name,
		Type:                deployments.DeploymentType(row.Type),
		Version:             row.Version,
		Status:              deployments.DeploymentStatus(row.Status),
		ContentCID:          row.ContentCID,
		BuildCID:            row.BuildCID,
		HomeNodeID:          row.HomeNodeID,
		Port:                row.Port,
		Subdomain:           row.Subdomain,
		Environment:         env,
		MemoryLimitMB:       row.MemoryLimitMB,
		CPULimitPercent:     row.CPULimitPercent,
		DiskLimitMB:         row.DiskLimitMB,
		HealthCheckPath:     row.HealthCheckPath,
		HealthCheckInterval: row.HealthCheckInterval,
		RestartPolicy:       deployments.RestartPolicy(row.RestartPolicy),
		MaxRestartCount:     row.MaxRestartCount,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		DeployedBy:          row.DeployedBy,
	}, nil
}

// GetDeploymentByID retrieves a deployment by namespace and ID
func (s *DeploymentService) GetDeploymentByID(ctx context.Context, namespace, id string) (*deployments.Deployment, error) {
	type deploymentRow struct {
		ID                  string    `db:"id"`
		Namespace           string    `db:"namespace"`
		Name                string    `db:"name"`
		Type                string    `db:"type"`
		Version             int       `db:"version"`
		Status              string    `db:"status"`
		ContentCID          string    `db:"content_cid"`
		BuildCID            string    `db:"build_cid"`
		HomeNodeID          string    `db:"home_node_id"`
		Port                int       `db:"port"`
		Subdomain           string    `db:"subdomain"`
		Environment         string    `db:"environment"`
		MemoryLimitMB       int       `db:"memory_limit_mb"`
		CPULimitPercent     int       `db:"cpu_limit_percent"`
		DiskLimitMB         int       `db:"disk_limit_mb"`
		HealthCheckPath     string    `db:"health_check_path"`
		HealthCheckInterval int       `db:"health_check_interval"`
		RestartPolicy       string    `db:"restart_policy"`
		MaxRestartCount     int       `db:"max_restart_count"`
		CreatedAt           time.Time `db:"created_at"`
		UpdatedAt           time.Time `db:"updated_at"`
		DeployedBy          string    `db:"deployed_by"`
	}

	var rows []deploymentRow
	query := `SELECT * FROM deployments WHERE namespace = ? AND id = ? LIMIT 1`
	err := s.db.Query(ctx, &rows, query, namespace, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment: %w", err)
	}

	if len(rows) == 0 {
		return nil, deployments.ErrDeploymentNotFound
	}

	row := rows[0]
	var env map[string]string
	if err := json.Unmarshal([]byte(row.Environment), &env); err != nil {
		env = make(map[string]string)
	}

	return &deployments.Deployment{
		ID:                  row.ID,
		Namespace:           row.Namespace,
		Name:                row.Name,
		Type:                deployments.DeploymentType(row.Type),
		Version:             row.Version,
		Status:              deployments.DeploymentStatus(row.Status),
		ContentCID:          row.ContentCID,
		BuildCID:            row.BuildCID,
		HomeNodeID:          row.HomeNodeID,
		Port:                row.Port,
		Subdomain:           row.Subdomain,
		Environment:         env,
		MemoryLimitMB:       row.MemoryLimitMB,
		CPULimitPercent:     row.CPULimitPercent,
		DiskLimitMB:         row.DiskLimitMB,
		HealthCheckPath:     row.HealthCheckPath,
		HealthCheckInterval: row.HealthCheckInterval,
		RestartPolicy:       deployments.RestartPolicy(row.RestartPolicy),
		MaxRestartCount:     row.MaxRestartCount,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		DeployedBy:          row.DeployedBy,
	}, nil
}

// UpdateDeploymentStatus updates the status of a deployment
func (s *DeploymentService) UpdateDeploymentStatus(ctx context.Context, deploymentID string, status deployments.DeploymentStatus) error {
	query := `UPDATE deployments SET status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(ctx, query, status, time.Now(), deploymentID)
	if err != nil {
		s.logger.Error("Failed to update deployment status",
			zap.String("deployment_id", deploymentID),
			zap.String("status", string(status)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update deployment status: %w", err)
	}
	return nil
}

// CreateDNSRecords creates DNS records for a deployment
func (s *DeploymentService) CreateDNSRecords(ctx context.Context, deployment *deployments.Deployment) error {
	// Get node IP using the full node ID
	nodeIP, err := s.getNodeIP(ctx, deployment.HomeNodeID)
	if err != nil {
		s.logger.Error("Failed to get node IP", zap.Error(err))
		return err
	}

	// Use subdomain if set, otherwise fall back to name
	// New format: {name}-{random}.{baseDomain} (e.g., myapp-f3o4if.dbrs.space)
	dnsName := deployment.Subdomain
	if dnsName == "" {
		dnsName = deployment.Name
	}

	// Create deployment record: {subdomain}.{baseDomain}
	// Any node can receive the request and proxy to the home node if needed
	fqdn := fmt.Sprintf("%s.%s.", dnsName, s.BaseDomain())
	if err := s.createDNSRecord(ctx, fqdn, "A", nodeIP, deployment.Namespace, deployment.ID); err != nil {
		s.logger.Error("Failed to create DNS record", zap.Error(err))
	} else {
		s.logger.Info("Created DNS record",
			zap.String("fqdn", fqdn),
			zap.String("ip", nodeIP),
			zap.String("subdomain", dnsName),
		)
	}

	return nil
}

// createDNSRecord creates a single DNS record
func (s *DeploymentService) createDNSRecord(ctx context.Context, fqdn, recordType, value, namespace, deploymentID string) error {
	query := `
		INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, deployment_id, is_active, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fqdn, record_type, value) DO UPDATE SET
			deployment_id = excluded.deployment_id,
			updated_at = excluded.updated_at,
			is_active = TRUE
	`

	now := time.Now()
	_, err := s.db.Exec(ctx, query, fqdn, recordType, value, 300, namespace, deploymentID, true, now, now, "system")
	return err
}

// getNodeIP retrieves the IP address for a node.
// It tries to find the node by full peer ID first, then by short node ID.
func (s *DeploymentService) getNodeIP(ctx context.Context, nodeID string) (string, error) {
	type nodeRow struct {
		IPAddress string `db:"ip_address"`
	}

	var rows []nodeRow

	// Try full node ID first
	query := `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	err := s.db.Query(ctx, &rows, query, nodeID)
	if err != nil {
		return "", err
	}

	// If found, return it
	if len(rows) > 0 {
		return rows[0].IPAddress, nil
	}

	// Try with short node ID if the original was a full peer ID
	shortID := GetShortNodeID(nodeID)
	if shortID != nodeID {
		err = s.db.Query(ctx, &rows, query, shortID)
		if err != nil {
			return "", err
		}
		if len(rows) > 0 {
			return rows[0].IPAddress, nil
		}
	}

	return "", fmt.Errorf("node not found: %s (tried: %s, %s)", nodeID, nodeID, shortID)
}

// BuildDeploymentURLs builds all URLs for a deployment
func (s *DeploymentService) BuildDeploymentURLs(deployment *deployments.Deployment) []string {
	// Use subdomain if set, otherwise fall back to name
	// New format: {name}-{random}.{baseDomain} (e.g., myapp-f3o4if.dbrs.space)
	dnsName := deployment.Subdomain
	if dnsName == "" {
		dnsName = deployment.Name
	}
	return []string{
		fmt.Sprintf("https://%s.%s", dnsName, s.BaseDomain()),
	}
}

// recordHistory records deployment history
func (s *DeploymentService) recordHistory(ctx context.Context, deployment *deployments.Deployment, status string) {
	query := `
		INSERT INTO deployment_history (id, deployment_id, version, content_cid, build_cid, deployed_at, deployed_by, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(ctx, query,
		uuid.New().String(),
		deployment.ID,
		deployment.Version,
		deployment.ContentCID,
		deployment.BuildCID,
		time.Now(),
		deployment.DeployedBy,
		status,
	)

	if err != nil {
		s.logger.Error("Failed to record history", zap.Error(err))
	}
}
