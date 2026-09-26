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
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NodeJSHandler handles Node.js backend deployments
type NodeJSHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	ipfsClient     ipfs.IPFSClient
	logger         *zap.Logger
	baseDeployPath string
}

// NewNodeJSHandler creates a new Node.js deployment handler
func NewNodeJSHandler(
	service *DeploymentService,
	processManager *process.Manager,
	ipfsClient ipfs.IPFSClient,
	logger *zap.Logger,
	baseDeployPath string,
) *NodeJSHandler {
	if baseDeployPath == "" {
		baseDeployPath = filepath.Join(os.Getenv("HOME"), ".orama", "deployments")
	}
	return &NodeJSHandler{
		service:        service,
		processManager: processManager,
		ipfsClient:     ipfsClient,
		logger:         logger,
		baseDeployPath: baseDeployPath,
	}
}

// HandleUpload handles Node.js backend deployment upload
func (h *NodeJSHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Parse multipart form (200MB max for Node.js with node_modules)
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get metadata
	name := r.FormValue("name")
	subdomain := r.FormValue("subdomain")
	healthCheckPath := r.FormValue("health_check_path")
	skipInstall := r.FormValue("skip_install") == "true"

	if err := h.service.CheckNewDeploymentName(ctx, namespace, name); err != nil {
		writeDeploymentNameError(w, h.logger, err)
		return
	}

	// Environment variables arrive as env_<NAME> form fields. Only the Go
	// handler read these, so a Node.js app could not be configured at all.
	envVars, err := parseFormEnv(r.MultipartForm.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

	// Claim the instance on this host before anything is uploaded or written.
	claim, err := h.service.claimNewInstance(ctx, h.baseDeployPath, namespace, name)
	if err != nil {
		writeDeploymentNameError(w, h.logger, err)
		return
	}
	registered := false
	defer func() {
		if !registered {
			h.service.releaseClaim(claim)
		}
	}()

	h.logger.Info("Deploying Node.js backend",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
		zap.Bool("skip_install", skipInstall),
	)

	// Upload to IPFS for versioning
	addResp, err := h.ipfsClient.Add(ctx, file, header.Filename)
	if err != nil {
		h.logger.Error("Failed to upload to IPFS", zap.Error(err))
		http.Error(w, "Failed to upload content", http.StatusInternalServerError)
		return
	}

	cid := addResp.Cid

	// Deploy the Node.js backend
	deployment, err := h.deploy(ctx, namespace, name, subdomain, cid, healthCheckPath, skipInstall, envVars)
	// A deployment with a registry row keeps its directory even if it failed
	// to start: it exists, and a delete removes both.
	registered = deployment != nil
	if err != nil {
		h.logger.Error("Failed to deploy Node.js backend", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build response
	h.service.RecordAudit(r, deployment.Namespace, auth.AuditDeploymentCreated, deployment.Name)

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

// deploy deploys a Node.js backend
func (h *NodeJSHandler) deploy(ctx context.Context, namespace, name, subdomain, cid, healthCheckPath string, skipInstall bool, envVars map[string]string) (*deployments.Deployment, error) {
	// The directory exists: HandleUpload claimed it.
	deployPath := process.DeployDir(h.baseDeployPath, namespace, name)

	// Download and extract from IPFS
	if err := h.extractFromIPFS(ctx, cid, deployPath); err != nil {
		return nil, fmt.Errorf("failed to extract deployment: %w", err)
	}

	// Check for package.json
	packageJSONPath := filepath.Join(deployPath, "package.json")
	if _, err := os.Stat(packageJSONPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("package.json not found in deployment")
	}

	// Install dependencies if the app did not ship them. The install is the
	// tenant's (npm reads its package.json, lockfile and registry), so it runs
	// in its own sandboxed unit, never in this process (process/build.go).
	// Without an install, whatever an earlier holder of this instance
	// installed is removed: the runtime unit would bind it under the app.
	install, err := needsInstall(filepath.Join(deployPath, "node_modules"), skipInstall)
	if err != nil {
		return nil, err
	}
	if install {
		h.logger.Info("Installing npm dependencies", zap.String("deployment", name))
		if err := h.processManager.InstallDependencies(ctx, namespace, name, deployPath); err != nil {
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	} else if err := h.processManager.ClearDependencies(namespace, name); err != nil {
		return nil, err
	}

	// Parse package.json to determine entry point
	entryPoint, err := h.determineEntryPoint(deployPath)
	if err != nil {
		h.logger.Warn("Failed to determine entry point, using default",
			zap.Error(err),
			zap.String("default", "index.js"),
		)
		entryPoint = "index.js"
	}

	h.logger.Info("Node.js deployment configured",
		zap.String("entry_point", entryPoint),
		zap.String("deployment", name),
	)

	// Create deployment record
	deployment := &deployments.Deployment{
		ID:                  uuid.New().String(),
		Namespace:           namespace,
		Name:                name,
		Type:                deployments.DeploymentTypeNodeJSBackend,
		Version:             1,
		Status:              deployments.DeploymentStatusDeploying,
		ContentCID:          cid,
		Subdomain:           subdomain,
		Environment:         withEntryPoint(envVars, entryPoint),
		MemoryLimitMB:       512,
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
	if err := h.processManager.WaitForHealthy(ctx, deployment, 90*time.Second); err != nil {
		h.logger.Warn("Deployment did not become healthy", zap.Error(err))
		// Don't fail - the service might still be starting
	}

	deployment.Status = deployments.DeploymentStatusActive
	h.service.UpdateDeploymentStatus(ctx, deployment.ID, deployments.DeploymentStatusActive)

	return deployment, nil
}

// extractFromIPFS extracts a tarball from IPFS to a directory
func (h *NodeJSHandler) extractFromIPFS(ctx context.Context, cid, destPath string) error {
	// Get tarball from IPFS
	reader, err := h.ipfsClient.Get(ctx, "/ipfs/"+cid, "")
	if err != nil {
		return err
	}
	defer reader.Close()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "nodejs-deploy-*.tar.gz")
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
	cmd := exec.Command("tar", tarExtractArgs(tmpFile.Name(), destPath)...)
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

// needsInstall reports whether the server installs the dependencies: the
// tenant did not skip it and did not ship node_modules.
func needsInstall(nodeModulesPath string, skipInstall bool) (bool, error) {
	if skipInstall {
		return false, nil
	}
	_, err := os.Lstat(nodeModulesPath)
	switch {
	case err == nil:
		return false, nil
	case os.IsNotExist(err):
		return true, nil
	default:
		return false, fmt.Errorf("inspect %s: %w", nodeModulesPath, err)
	}
}

// determineEntryPoint reads package.json to find the entry point
//
// package.json is the tenant's file: it is read as a bounded regular file,
// never through a symlink or from a FIFO that would block this request.
func (h *NodeJSHandler) determineEntryPoint(deployPath string) (string, error) {
	data, err := process.ReadManifest(deployPath)
	if err != nil {
		return "", fmt.Errorf("read package.json: %w", err)
	}

	var pkg struct {
		Main    string            `json:"main"`
		Scripts map[string]string `json:"scripts"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}

	// Check if there's a start script
	if startScript, ok := pkg.Scripts["start"]; ok {
		// If start script uses node, extract the file
		if len(startScript) > 5 && startScript[:5] == "node " {
			return startScript[5:], nil
		}
		// Otherwise, we'll use npm start
		return "npm:start", nil
	}

	// Use main field if specified
	if pkg.Main != "" {
		return pkg.Main, nil
	}

	// Default to index.js
	return "index.js", nil
}
