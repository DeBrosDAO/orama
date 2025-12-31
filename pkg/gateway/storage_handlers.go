package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// StorageUploadRequest represents a request to upload content to IPFS
type StorageUploadRequest struct {
	Name string `json:"name,omitempty"`
	Data string `json:"data,omitempty"` // Base64 encoded data (alternative to multipart)
}

// StorageUploadResponse represents the response from uploading content
type StorageUploadResponse struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// StoragePinRequest represents a request to pin a CID
type StoragePinRequest struct {
	Cid  string `json:"cid"`
	Name string `json:"name,omitempty"`
}

// StoragePinResponse represents the response from pinning a CID
type StoragePinResponse struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
}

// StorageStatusResponse represents the status of a pinned CID
type StorageStatusResponse struct {
	Cid               string   `json:"cid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	ReplicationMin    int      `json:"replication_min"`
	ReplicationMax    int      `json:"replication_max"`
	ReplicationFactor int      `json:"replication_factor"`
	Peers             []string `json:"peers"`
	Error             string   `json:"error,omitempty"`
}

// storageUploadHandler handles POST /v1/storage/upload
func (g *Gateway) storageUploadHandler(w http.ResponseWriter, r *http.Request) {
	if g.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get namespace from context
	namespace := g.getNamespaceFromContext(r.Context())
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "namespace required")
		return
	}

	// Get replication factor from config (default: 3)
	replicationFactor := g.cfg.IPFSReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	// Check if it's multipart/form-data or JSON
	contentType := r.Header.Get("Content-Type")
	var reader io.Reader
	var name string
	var shouldPin bool = true // Default to true

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Handle multipart upload
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse multipart form: %v", err))
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to get file: %v", err))
			return
		}
		defer file.Close()

		reader = file
		name = header.Filename

		// Parse pin flag from form (default: true)
		if pinValue := r.FormValue("pin"); pinValue != "" {
			shouldPin = strings.ToLower(pinValue) == "true"
		}
	} else {
		// Handle JSON request with base64 data
		var req StorageUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode request: %v", err))
			return
		}

		if req.Data == "" {
			writeError(w, http.StatusBadRequest, "data field required")
			return
		}

		// Decode base64 data
		data, err := base64Decode(req.Data)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode base64 data: %v", err))
			return
		}

		reader = bytes.NewReader(data)
		name = req.Name
		// For JSON requests, pin defaults to true (can be extended if needed)
	}

	// Add to IPFS
	ctx := r.Context()
	addResp, err := g.ipfsClient.Add(ctx, reader, name)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to add content to IPFS", zap.Error(err))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to add content: %v", err))
		return
	}

	// Return response immediately - don't block on pinning
	response := StorageUploadResponse{
		Cid:  addResp.Cid,
		Name: addResp.Name,
		Size: addResp.Size,
	}

	// Pin asynchronously in background if requested
	if shouldPin {
		go g.pinAsync(addResp.Cid, name, replicationFactor)
	}

	writeJSON(w, http.StatusOK, response)
}

// storagePinHandler handles POST /v1/storage/pin
func (g *Gateway) storagePinHandler(w http.ResponseWriter, r *http.Request) {
	if g.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req StoragePinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode request: %v", err))
		return
	}

	if req.Cid == "" {
		writeError(w, http.StatusBadRequest, "cid required")
		return
	}

	// Get replication factor from config (default: 3)
	replicationFactor := g.cfg.IPFSReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	ctx := r.Context()
	pinResp, err := g.ipfsClient.Pin(ctx, req.Cid, req.Name, replicationFactor)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to pin CID", zap.Error(err), zap.String("cid", req.Cid))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to pin: %v", err))
		return
	}

	// Use name from request if response doesn't have it
	name := pinResp.Name
	if name == "" {
		name = req.Name
	}

	response := StoragePinResponse{
		Cid:  pinResp.Cid,
		Name: name,
	}

	writeJSON(w, http.StatusOK, response)
}

// storageStatusHandler handles GET /v1/storage/status/:cid
func (g *Gateway) storageStatusHandler(w http.ResponseWriter, r *http.Request) {
	if g.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/status/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "cid required")
		return
	}

	ctx := r.Context()
	status, err := g.ipfsClient.PinStatus(ctx, path)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to get pin status", zap.Error(err), zap.String("cid", path))
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("pin not found: %s", path))
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get status: %v", err))
		}
		return
	}

	response := StorageStatusResponse{
		Cid:               status.Cid,
		Name:              status.Name,
		Status:            status.Status,
		ReplicationMin:    status.ReplicationMin,
		ReplicationMax:    status.ReplicationMax,
		ReplicationFactor: status.ReplicationFactor,
		Peers:             status.Peers,
		Error:             status.Error,
	}

	writeJSON(w, http.StatusOK, response)
}

// storageGetHandler handles GET /v1/storage/get/:cid
func (g *Gateway) storageGetHandler(w http.ResponseWriter, r *http.Request) {
	if g.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/get/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "cid required")
		return
	}

	// Get namespace from context
	namespace := g.getNamespaceFromContext(r.Context())
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "namespace required")
		return
	}

	// Get IPFS API URL from config
	ipfsAPIURL := g.cfg.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:5001"
	}

	ctx := r.Context()
	reader, err := g.ipfsClient.Get(ctx, path, ipfsAPIURL)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to get content from IPFS", zap.Error(err), zap.String("cid", path))
		// Check if error indicates content not found (404)
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			writeError(w, http.StatusNotFound, fmt.Sprintf("content not found: %s", path))
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get content: %v", err))
		}
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", path))

	if _, err := io.Copy(w, reader); err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to write content", zap.Error(err))
	}
}

// storageUnpinHandler handles DELETE /v1/storage/unpin/:cid
func (g *Gateway) storageUnpinHandler(w http.ResponseWriter, r *http.Request) {
	if g.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/unpin/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "cid required")
		return
	}

	ctx := r.Context()
	if err := g.ipfsClient.Unpin(ctx, path); err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to unpin CID", zap.Error(err), zap.String("cid", path))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to unpin: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path})
}

// pinAsync pins a CID asynchronously in the background with retry logic
// Retries once if the first attempt fails, then gives up
func (g *Gateway) pinAsync(cid, name string, replicationFactor int) {
	ctx := context.Background()

	// First attempt
	_, err := g.ipfsClient.Pin(ctx, cid, name, replicationFactor)
	if err == nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "async pin succeeded", zap.String("cid", cid))
		return
	}

	// Log first failure
	g.logger.ComponentWarn(logging.ComponentGeneral, "async pin failed, retrying once",
		zap.Error(err), zap.String("cid", cid))

	// Retry once after a short delay
	time.Sleep(2 * time.Second)
	_, err = g.ipfsClient.Pin(ctx, cid, name, replicationFactor)
	if err != nil {
		// Final failure - log and give up
		g.logger.ComponentWarn(logging.ComponentGeneral, "async pin retry failed, giving up",
			zap.Error(err), zap.String("cid", cid))
	} else {
		g.logger.ComponentWarn(logging.ComponentGeneral, "async pin succeeded on retry", zap.String("cid", cid))
	}
}

// base64Decode decodes base64 string to bytes
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// getNamespaceFromContext extracts namespace from request context
func (g *Gateway) getNamespaceFromContext(ctx context.Context) string {
	if v := ctx.Value(ctxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// Network HTTP handlers

func (g *Gateway) networkStatusHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	// Use internal auth context to bypass client credential requirements
	ctx := client.WithInternalAuth(r.Context())
	status, err := g.client.Network().GetStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Override with the node's actual peer ID if available
	// (the client's embedded host has a different temporary peer ID)
	if g.nodePeerID != "" {
		status.PeerID = g.nodePeerID
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) networkPeersHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	// Use internal auth context to bypass client credential requirements
	ctx := client.WithInternalAuth(r.Context())
	peers, err := g.client.Network().GetPeers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Flatten peer addresses into a list of multiaddr strings
	// Each PeerInfo can have multiple addresses, so we collect all of them
	peerAddrs := make([]string, 0)
	for _, peer := range peers {
		// Add peer ID as /p2p/ multiaddr format
		if peer.ID != "" {
			peerAddrs = append(peerAddrs, "/p2p/"+peer.ID)
		}
		// Add all addresses for this peer
		peerAddrs = append(peerAddrs, peer.Addresses...)
	}
	// Return peers in expected format: {"peers": ["/p2p/...", "/ip4/...", ...]}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peerAddrs})
}

func (g *Gateway) networkConnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Multiaddr string `json:"multiaddr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Multiaddr == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {multiaddr}")
		return
	}
	if err := g.client.Network().ConnectToPeer(r.Context(), body.Multiaddr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) networkDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {peer_id}")
		return
	}
	if err := g.client.Network().DisconnectFromPeer(r.Context(), body.PeerID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
