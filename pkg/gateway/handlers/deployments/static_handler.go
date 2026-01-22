package deployments

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// getNamespaceFromContext extracts the namespace from the request context
// Returns empty string if namespace is not found
func getNamespaceFromContext(ctx context.Context) string {
	if ns, ok := ctx.Value(ctxkeys.NamespaceOverride).(string); ok {
		return ns
	}
	return ""
}

// StaticDeploymentHandler handles static site deployments
type StaticDeploymentHandler struct {
	service    *DeploymentService
	ipfsClient ipfs.IPFSClient
	logger     *zap.Logger
}

// NewStaticDeploymentHandler creates a new static deployment handler
func NewStaticDeploymentHandler(service *DeploymentService, ipfsClient ipfs.IPFSClient, logger *zap.Logger) *StaticDeploymentHandler {
	return &StaticDeploymentHandler{
		service:    service,
		ipfsClient: ipfsClient,
		logger:     logger,
	}
}

// HandleUpload handles static site upload and deployment
func (h *StaticDeploymentHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get namespace from context (set by auth middleware)
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB max
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get deployment metadata
	name := r.FormValue("name")
	subdomain := r.FormValue("subdomain")
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

	// Validate file extension
	if !strings.HasSuffix(header.Filename, ".tar.gz") && !strings.HasSuffix(header.Filename, ".tgz") {
		http.Error(w, "File must be a .tar.gz or .tgz archive", http.StatusBadRequest)
		return
	}

	h.logger.Info("Uploading static site",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	// Extract tarball to temporary directory
	// Create a wrapper directory so IPFS creates a root CID
	tmpDir, err := os.MkdirTemp("", "static-deploy-*")
	if err != nil {
		h.logger.Error("Failed to create temp directory", zap.Error(err))
		http.Error(w, "Failed to process tarball", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	// Extract into a subdirectory called "site" so we get a root directory CID
	siteDir := filepath.Join(tmpDir, "site")
	if err := os.MkdirAll(siteDir, 0755); err != nil {
		h.logger.Error("Failed to create site directory", zap.Error(err))
		http.Error(w, "Failed to process tarball", http.StatusInternalServerError)
		return
	}

	if err := extractTarball(file, siteDir); err != nil {
		h.logger.Error("Failed to extract tarball", zap.Error(err))
		http.Error(w, "Failed to extract tarball", http.StatusInternalServerError)
		return
	}

	// Upload the parent directory (tmpDir) to IPFS, which will create a CID for the "site" subdirectory
	addResp, err := h.ipfsClient.AddDirectory(ctx, tmpDir)
	if err != nil {
		h.logger.Error("Failed to upload to IPFS", zap.Error(err))
		http.Error(w, "Failed to upload content", http.StatusInternalServerError)
		return
	}

	cid := addResp.Cid

	h.logger.Info("Content uploaded to IPFS",
		zap.String("cid", cid),
		zap.String("namespace", namespace),
		zap.String("name", name),
	)

	// Create deployment
	deployment := &deployments.Deployment{
		ID:          uuid.New().String(),
		Namespace:   namespace,
		Name:        name,
		Type:        deployments.DeploymentTypeStatic,
		Version:     1,
		Status:      deployments.DeploymentStatusActive,
		ContentCID:  cid,
		Subdomain:   subdomain,
		Environment: make(map[string]string),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		DeployedBy:  namespace,
	}

	// Save deployment
	if err := h.service.CreateDeployment(ctx, deployment); err != nil {
		h.logger.Error("Failed to create deployment", zap.Error(err))
		http.Error(w, "Failed to create deployment", http.StatusInternalServerError)
		return
	}

	// Create DNS records
	go h.service.CreateDNSRecords(ctx, deployment)

	// Build URLs
	urls := h.service.BuildDeploymentURLs(deployment)

	// Return response
	resp := map[string]interface{}{
		"deployment_id": deployment.ID,
		"name":          deployment.Name,
		"namespace":     deployment.Namespace,
		"status":        deployment.Status,
		"content_cid":   deployment.ContentCID,
		"urls":          urls,
		"version":       deployment.Version,
		"created_at":    deployment.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// HandleServe serves static content from IPFS
func (h *StaticDeploymentHandler) HandleServe(w http.ResponseWriter, r *http.Request, deployment *deployments.Deployment) {
	ctx := r.Context()

	// Get requested path
	requestPath := r.URL.Path
	if requestPath == "" || requestPath == "/" {
		requestPath = "/index.html"
	}

	// Build IPFS path
	ipfsPath := fmt.Sprintf("/ipfs/%s%s", deployment.ContentCID, requestPath)

	h.logger.Debug("Serving static content",
		zap.String("deployment", deployment.Name),
		zap.String("path", requestPath),
		zap.String("ipfs_path", ipfsPath),
	)

	// Try to get the file
	reader, err := h.ipfsClient.Get(ctx, ipfsPath, "")
	if err != nil {
		// Try with /index.html for directories
		if !strings.HasSuffix(requestPath, ".html") {
			indexPath := fmt.Sprintf("/ipfs/%s%s/index.html", deployment.ContentCID, requestPath)
			reader, err = h.ipfsClient.Get(ctx, indexPath, "")
		}

		// Fallback to /index.html for SPA routing
		if err != nil {
			fallbackPath := fmt.Sprintf("/ipfs/%s/index.html", deployment.ContentCID)
			reader, err = h.ipfsClient.Get(ctx, fallbackPath, "")
			if err != nil {
				h.logger.Error("Failed to serve content", zap.Error(err))
				http.NotFound(w, r)
				return
			}
		}
	}
	defer reader.Close()

	// Detect content type
	contentType := detectContentType(requestPath)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")

	// Copy content to response
	if _, err := io.Copy(w, reader); err != nil {
		h.logger.Error("Failed to write response", zap.Error(err))
	}
}

// detectContentType determines content type from file extension
func detectContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	types := map[string]string{
		".html": "text/html; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".js":   "application/javascript; charset=utf-8",
		".json": "application/json",
		".xml":  "application/xml",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".ico":  "image/x-icon",
		".woff": "font/woff",
		".woff2": "font/woff2",
		".ttf":  "font/ttf",
		".eot":  "application/vnd.ms-fontobject",
		".txt":  "text/plain; charset=utf-8",
		".pdf":  "application/pdf",
		".zip":  "application/zip",
	}

	if contentType, ok := types[ext]; ok {
		return contentType
	}

	return "application/octet-stream"
}

// extractTarball extracts a .tar.gz file to the specified directory
func extractTarball(reader io.Reader, destDir string) error {
	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Build target path
		target := filepath.Join(destDir, header.Name)

		// Prevent path traversal - clean both paths before comparing
		cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, cleanDest) && cleanTarget != filepath.Clean(destDir) {
			return fmt.Errorf("invalid file path in tarball: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			// Create parent directory if needed
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %w", err)
			}

			// Create file
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			f.Close()
		}
	}

	return nil
}

