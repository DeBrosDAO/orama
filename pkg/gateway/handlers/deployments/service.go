package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DeploymentService manages deployment operations
type DeploymentService struct {
	db              rqlite.Client
	homeNodeManager *deployments.HomeNodeManager
	portAllocator   *deployments.PortAllocator
	logger          *zap.Logger
	baseDomain      string // Base domain for deployments (e.g., "dbrs.space")
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
		baseDomain:      "orama.network", // default
	}
}

// SetBaseDomain sets the base domain for deployments
func (s *DeploymentService) SetBaseDomain(domain string) {
	if domain != "" {
		s.baseDomain = domain
	}
}

// BaseDomain returns the configured base domain
func (s *DeploymentService) BaseDomain() string {
	if s.baseDomain == "" {
		return "orama.network"
	}
	return s.baseDomain
}

// CreateDeployment creates a new deployment
func (s *DeploymentService) CreateDeployment(ctx context.Context, deployment *deployments.Deployment) error {
	// Assign home node if not already assigned
	if deployment.HomeNodeID == "" {
		homeNodeID, err := s.homeNodeManager.AssignHomeNode(ctx, deployment.Namespace)
		if err != nil {
			return fmt.Errorf("failed to assign home node: %w", err)
		}
		deployment.HomeNodeID = homeNodeID
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

// CreateDNSRecords creates DNS records for a deployment
func (s *DeploymentService) CreateDNSRecords(ctx context.Context, deployment *deployments.Deployment) error {
	// Get node IP
	nodeIP, err := s.getNodeIP(ctx, deployment.HomeNodeID)
	if err != nil {
		s.logger.Error("Failed to get node IP", zap.Error(err))
		return err
	}

	// Create node-specific record
	nodeFQDN := fmt.Sprintf("%s.%s.%s.", deployment.Name, deployment.HomeNodeID, s.BaseDomain())
	if err := s.createDNSRecord(ctx, nodeFQDN, "A", nodeIP, deployment.Namespace, deployment.ID); err != nil {
		s.logger.Error("Failed to create node-specific DNS record", zap.Error(err))
	}

	// Create load-balanced record if subdomain is set
	if deployment.Subdomain != "" {
		lbFQDN := fmt.Sprintf("%s.%s.", deployment.Subdomain, s.BaseDomain())
		if err := s.createDNSRecord(ctx, lbFQDN, "A", nodeIP, deployment.Namespace, deployment.ID); err != nil {
			s.logger.Error("Failed to create load-balanced DNS record", zap.Error(err))
		}
	}

	return nil
}

// createDNSRecord creates a single DNS record
func (s *DeploymentService) createDNSRecord(ctx context.Context, fqdn, recordType, value, namespace, deploymentID string) error {
	query := `
		INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, deployment_id, is_active, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fqdn) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`

	now := time.Now()
	_, err := s.db.Exec(ctx, query, fqdn, recordType, value, 300, namespace, deploymentID, true, now, now, "system")
	return err
}

// getNodeIP retrieves the IP address for a node
func (s *DeploymentService) getNodeIP(ctx context.Context, nodeID string) (string, error) {
	type nodeRow struct {
		IPAddress string `db:"ip_address"`
	}

	var rows []nodeRow
	query := `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	err := s.db.Query(ctx, &rows, query, nodeID)
	if err != nil {
		return "", err
	}

	if len(rows) == 0 {
		return "", fmt.Errorf("node not found: %s", nodeID)
	}

	return rows[0].IPAddress, nil
}

// BuildDeploymentURLs builds all URLs for a deployment
func (s *DeploymentService) BuildDeploymentURLs(deployment *deployments.Deployment) []string {
	urls := []string{
		fmt.Sprintf("https://%s.%s.%s", deployment.Name, deployment.HomeNodeID, s.BaseDomain()),
	}

	if deployment.Subdomain != "" {
		urls = append(urls, fmt.Sprintf("https://%s.%s", deployment.Subdomain, s.BaseDomain()))
	}

	return urls
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
