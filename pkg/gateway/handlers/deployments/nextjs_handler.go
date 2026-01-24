package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NextJSHandler handles Next.js deployments
type NextJSHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	ipfsClient     ipfs.IPFSClient
	logger         *zap.Logger
	baseDeployPath string
}

// NewNextJSHandler creates a new Next.js deployment handler
func NewNextJSHandler(
	service *DeploymentService,
	processManager *process.Manager,
	ipfsClient ipfs.IPFSClient,
	logger *zap.Logger,
) *NextJSHandler {
	return &NextJSHandler{
		service:        service,
		processManager: processManager,
		ipfsClient:     ipfsClient,
		logger:         logger,
		baseDeployPath: "/home/debros/.orama/deployments",
	}
}

// HandleUpload handles Next.js deployment upload
func (h *NextJSHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(200 << 20); err != nil { // 200MB max
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get metadata
	name := r.FormValue("name")
	subdomain := r.FormValue("subdomain")
	sseMode := r.FormValue("ssr") == "true"

	if name == "" {
		http.Error(w, "Deployment name is required", http.StatusBadRequest)
		return
	}

	// Get tarball file
	file, header, err := r.FormFile("tarball")
	if err != nil {
		http.Error(w, "Tarball file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	h.logger.Info("Deploying Next.js application",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Bool("ssr", sseMode),
	)

	// Upload to IPFS
	addResp, err := h.ipfsClient.Add(ctx, file, header.Filename)
	if err != nil {
		h.logger.Error("Failed to upload to IPFS", zap.Error(err))
		http.Error(w, "Failed to upload content", http.StatusInternalServerError)
		return
	}

	cid := addResp.Cid

	var deployment *deployments.Deployment

	if sseMode {
		// SSR mode - extract and run as process
		deployment, err = h.deploySSR(ctx, namespace, name, subdomain, cid)
	} else {
		// Static export mode
		deployment, err = h.deployStatic(ctx, namespace, name, subdomain, cid)
	}

	if err != nil {
		h.logger.Error("Failed to deploy Next.js", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create DNS records (use background context since HTTP context will be cancelled)
	go h.service.CreateDNSRecords(context.Background(), deployment)

	// Build response
	urls := h.service.BuildDeploymentURLs(deployment)

	resp := map[string]interface{}{
		"deployment_id": deployment.ID,
		"name":          deployment.Name,
		"namespace":     deployment.Namespace,
		"status":        deployment.Status,
		"type":          deployment.Type,
		"content_cid":   deployment.ContentCID,
		"urls":          urls,
		"version":       deployment.Version,
		"port":          deployment.Port,
		"created_at":    deployment.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// deploySSR deploys Next.js in SSR mode
func (h *NextJSHandler) deploySSR(ctx context.Context, namespace, name, subdomain, cid string) (*deployments.Deployment, error) {
	// Create deployment directory
	deployPath := filepath.Join(h.baseDeployPath, namespace, name)
	if err := os.MkdirAll(deployPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Download and extract from IPFS
	if err := h.extractFromIPFS(ctx, cid, deployPath); err != nil {
		return nil, fmt.Errorf("failed to extract deployment: %w", err)
	}

	// Create deployment record
	deployment := &deployments.Deployment{
		ID:                  uuid.New().String(),
		Namespace:           namespace,
		Name:                name,
		Type:                deployments.DeploymentTypeNextJS,
		Version:             1,
		Status:              deployments.DeploymentStatusDeploying,
		ContentCID:          cid,
		Subdomain:           subdomain,
		Environment:         make(map[string]string),
		MemoryLimitMB:       512,
		CPULimitPercent:     100,
		HealthCheckPath:     "/api/health",
		HealthCheckInterval: 30,
		RestartPolicy:       deployments.RestartPolicyAlways,
		MaxRestartCount:     10,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		DeployedBy:          namespace,
	}

	// Save deployment (assigns port)
	if err := h.service.CreateDeployment(ctx, deployment); err != nil {
		return nil, err
	}

	// Start the process
	if err := h.processManager.Start(ctx, deployment, deployPath); err != nil {
		deployment.Status = deployments.DeploymentStatusFailed
		return deployment, fmt.Errorf("failed to start process: %w", err)
	}

	// Wait for healthy
	if err := h.processManager.WaitForHealthy(ctx, deployment, 60*time.Second); err != nil {
		h.logger.Warn("Deployment did not become healthy", zap.Error(err))
	}

	deployment.Status = deployments.DeploymentStatusActive

	// Update status in database
	if err := h.service.UpdateDeploymentStatus(ctx, deployment.ID, deployment.Status); err != nil {
		h.logger.Warn("Failed to update deployment status", zap.Error(err))
	}

	return deployment, nil
}

// deployStatic deploys Next.js static export
func (h *NextJSHandler) deployStatic(ctx context.Context, namespace, name, subdomain, cid string) (*deployments.Deployment, error) {
	deployment := &deployments.Deployment{
		ID:          uuid.New().String(),
		Namespace:   namespace,
		Name:        name,
		Type:        deployments.DeploymentTypeNextJSStatic,
		Version:     1,
		Status:      deployments.DeploymentStatusActive,
		ContentCID:  cid,
		Subdomain:   subdomain,
		Environment: make(map[string]string),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		DeployedBy:  namespace,
	}

	if err := h.service.CreateDeployment(ctx, deployment); err != nil {
		return nil, err
	}

	return deployment, nil
}

// extractFromIPFS extracts a tarball from IPFS to a directory
func (h *NextJSHandler) extractFromIPFS(ctx context.Context, cid, destPath string) error {
	// Get tarball from IPFS
	reader, err := h.ipfsClient.Get(ctx, "/ipfs/"+cid, "")
	if err != nil {
		return err
	}
	defer reader.Close()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "nextjs-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Copy to temp file
	if _, err := io.Copy(tmpFile, reader); err != nil {
		return err
	}

	tmpFile.Close()

	// Extract tarball
	cmd := fmt.Sprintf("tar -xzf %s -C %s", tmpFile.Name(), destPath)
	if err := h.execCommand(cmd); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}

	return nil
}

// execCommand executes a shell command
func (h *NextJSHandler) execCommand(cmd string) error {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	c := exec.Command(parts[0], parts[1:]...)
	output, err := c.CombinedOutput()
	if err != nil {
		h.logger.Error("Command execution failed",
			zap.String("command", cmd),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("command failed: %s: %w", string(output), err)
	}

	return nil
}
