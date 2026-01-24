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

// GoHandler handles Go backend deployments
type GoHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	ipfsClient     ipfs.IPFSClient
	logger         *zap.Logger
	baseDeployPath string
}

// NewGoHandler creates a new Go deployment handler
func NewGoHandler(
	service *DeploymentService,
	processManager *process.Manager,
	ipfsClient ipfs.IPFSClient,
	logger *zap.Logger,
) *GoHandler {
	return &GoHandler{
		service:        service,
		processManager: processManager,
		ipfsClient:     ipfsClient,
		logger:         logger,
		baseDeployPath: "/home/debros/.orama/deployments",
	}
}

// HandleUpload handles Go backend deployment upload
func (h *GoHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Parse multipart form (100MB max for Go binaries)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get metadata
	name := r.FormValue("name")
	subdomain := r.FormValue("subdomain")
	healthCheckPath := r.FormValue("health_check_path")

	if name == "" {
		http.Error(w, "Deployment name is required", http.StatusBadRequest)
		return
	}

	if healthCheckPath == "" {
		healthCheckPath = "/health"
	}

	// Get tarball file
	file, header, err := r.FormFile("tarball")
	if err != nil {
		http.Error(w, "Tarball file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	h.logger.Info("Deploying Go backend",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	// Upload to IPFS for versioning
	addResp, err := h.ipfsClient.Add(ctx, file, header.Filename)
	if err != nil {
		h.logger.Error("Failed to upload to IPFS", zap.Error(err))
		http.Error(w, "Failed to upload content", http.StatusInternalServerError)
		return
	}

	cid := addResp.Cid

	// Deploy the Go backend
	deployment, err := h.deploy(ctx, namespace, name, subdomain, cid, healthCheckPath)
	if err != nil {
		h.logger.Error("Failed to deploy Go backend", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create DNS records
	go h.service.CreateDNSRecords(ctx, deployment)

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

// deploy deploys a Go backend
func (h *GoHandler) deploy(ctx context.Context, namespace, name, subdomain, cid, healthCheckPath string) (*deployments.Deployment, error) {
	// Create deployment directory
	deployPath := filepath.Join(h.baseDeployPath, namespace, name)
	if err := os.MkdirAll(deployPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Download and extract from IPFS
	if err := h.extractFromIPFS(ctx, cid, deployPath); err != nil {
		return nil, fmt.Errorf("failed to extract deployment: %w", err)
	}

	// Find the executable binary
	binaryPath, err := h.findBinary(deployPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find binary: %w", err)
	}

	// Ensure binary is executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to make binary executable: %w", err)
	}

	h.logger.Info("Found Go binary",
		zap.String("path", binaryPath),
		zap.String("deployment", name),
	)

	// Create deployment record
	deployment := &deployments.Deployment{
		ID:                  uuid.New().String(),
		Namespace:           namespace,
		Name:                name,
		Type:                deployments.DeploymentTypeGoBackend,
		Version:             1,
		Status:              deployments.DeploymentStatusDeploying,
		ContentCID:          cid,
		Subdomain:           subdomain,
		Environment:         make(map[string]string),
		MemoryLimitMB:       256,
		CPULimitPercent:     100,
		HealthCheckPath:     healthCheckPath,
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
		h.service.UpdateDeploymentStatus(ctx, deployment.ID, deployments.DeploymentStatusFailed)
		return deployment, fmt.Errorf("failed to start process: %w", err)
	}

	// Wait for healthy
	if err := h.processManager.WaitForHealthy(ctx, deployment, 60*time.Second); err != nil {
		h.logger.Warn("Deployment did not become healthy", zap.Error(err))
		// Don't fail - the service might still be starting
	}

	deployment.Status = deployments.DeploymentStatusActive
	h.service.UpdateDeploymentStatus(ctx, deployment.ID, deployments.DeploymentStatusActive)

	return deployment, nil
}

// extractFromIPFS extracts a tarball from IPFS to a directory
func (h *GoHandler) extractFromIPFS(ctx context.Context, cid, destPath string) error {
	// Get tarball from IPFS
	reader, err := h.ipfsClient.Get(ctx, "/ipfs/"+cid, "")
	if err != nil {
		return err
	}
	defer reader.Close()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "go-deploy-*.tar.gz")
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
	cmd := exec.Command("tar", "-xzf", tmpFile.Name(), "-C", destPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.logger.Error("Failed to extract tarball",
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to extract tarball: %w", err)
	}

	return nil
}

// findBinary finds the Go binary in the deployment directory
func (h *GoHandler) findBinary(deployPath string) (string, error) {
	// First, look for a binary named "app" (conventional)
	appPath := filepath.Join(deployPath, "app")
	if info, err := os.Stat(appPath); err == nil && !info.IsDir() {
		return appPath, nil
	}

	// Look for any executable in the directory
	entries, err := os.ReadDir(deployPath)
	if err != nil {
		return "", fmt.Errorf("failed to read deployment directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(deployPath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Check if it's executable
		if info.Mode()&0111 != 0 {
			// Skip common non-binary files
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".sh" || ext == ".txt" || ext == ".md" || ext == ".json" || ext == ".yaml" || ext == ".yml" {
				continue
			}

			// Check if it's an ELF binary (Linux executable)
			if h.isELFBinary(filePath) {
				return filePath, nil
			}
		}
	}

	return "", fmt.Errorf("no executable binary found in deployment. Expected 'app' binary or ELF executable")
}

// isELFBinary checks if a file is an ELF binary
func (h *GoHandler) isELFBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Read first 4 bytes (ELF magic number)
	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return false
	}

	// ELF magic: 0x7f 'E' 'L' 'F'
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}
