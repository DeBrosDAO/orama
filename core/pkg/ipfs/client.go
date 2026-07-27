package ipfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// IPFSClient defines the interface for IPFS operations
type IPFSClient interface {
	Add(ctx context.Context, reader io.Reader, name string) (*AddResponse, error)
	AddDirectory(ctx context.Context, dirPath string) (*AddResponse, error)
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*PinResponse, error)
	PinStatus(ctx context.Context, cid string) (*PinStatus, error)
	Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error)
	Unpin(ctx context.Context, cid string) error
	EvictLocal(ctx context.Context, cid string) (int, error)
	Health(ctx context.Context) error
	GetPeerCount(ctx context.Context) (int, error)
	Close(ctx context.Context) error
}

// Client wraps an IPFS Cluster HTTP API client for storage operations
type Client struct {
	apiURL     string
	ipfsAPIURL string
	httpClient *http.Client
	logger     *zap.Logger
}

// Config holds configuration for the IPFS client
type Config struct {
	// ClusterAPIURL is the base URL for IPFS Cluster HTTP API (e.g., "http://localhost:9094")
	// If empty, defaults to "http://localhost:9094"
	ClusterAPIURL string

	// IPFSAPIURL is the base URL for IPFS daemon API (e.g., "http://localhost:4501")
	// Used for operations that require IPFS daemon directly (like directory uploads)
	IPFSAPIURL string

	// Timeout is the timeout for client operations
	// If zero, defaults to 60 seconds
	Timeout time.Duration
}

// PinStatus represents the status of a pinned CID
type PinStatus struct {
	Cid               string   `json:"cid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"` // "pinned", "pinning", "queued", "unpinned", "error"
	ReplicationMin    int      `json:"replication_min"`
	ReplicationMax    int      `json:"replication_max"`
	ReplicationFactor int      `json:"replication_factor"`
	Peers             []string `json:"peers"`
	PinnedPeers       int      `json:"pinned_peers"`
	TotalPeers        int      `json:"total_peers"`
	Error             string   `json:"error,omitempty"`
}

// Aggregated PinStatus.Status values. IPFS-Cluster reports a per-peer status;
// these are the honest cluster-wide rollups PinStatus computes from the peer_map.
const (
	PinStatusPinned  = "pinned"  // every peer holds the block
	PinStatusPinning = "pinning" // at least one peer is still fetching
	PinStatusError   = "error"   // at least one peer failed to pin
	PinStatusUnknown = "unknown" // no peers reported (cluster can't confirm)
)

// AddResponse represents the response from adding content to IPFS
type AddResponse struct {
	Name string `json:"name"`
	Cid  string `json:"cid"`
	Size int64  `json:"size"`
}

// ipfsDaemonAddResponse represents the response from IPFS daemon's /add endpoint
// The daemon returns Size as a string, unlike Cluster which returns it as int64
type ipfsDaemonAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"` // Daemon uses "Hash" instead of "Cid"
	Size string `json:"Size"` // Daemon returns size as string
}

// PinResponse represents the response from pinning a CID
type PinResponse struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
}

// NewClient creates a new IPFS Cluster client wrapper
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	apiURL := cfg.ClusterAPIURL
	if apiURL == "" {
		apiURL = "http://localhost:9094"
	}

	ipfsAPIURL := cfg.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:4501"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	httpClient := &http.Client{
		Timeout: timeout,
	}

	return &Client{
		apiURL:     apiURL,
		ipfsAPIURL: ipfsAPIURL,
		httpClient: httpClient,
		logger:     logger,
	}, nil
}

// Health checks if the IPFS Cluster API is healthy
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL+"/id", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status: %d", resp.StatusCode)
	}

	return nil
}

// GetPeerCount returns the number of cluster peers
func (c *Client) GetPeerCount(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL+"/peers", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create peers request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("peers request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("peers request failed with status: %d", resp.StatusCode)
	}

	// The /peers endpoint returns NDJSON (newline-delimited JSON), not a JSON array
	// We need to stream-read each peer object
	dec := json.NewDecoder(resp.Body)
	peerCount := 0
	for {
		var peer map[string]interface{}
		err := dec.Decode(&peer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, fmt.Errorf("failed to decode peers response: %w", err)
		}
		peerCount++
	}

	return peerCount, nil
}

// Add adds content to IPFS and returns the CID
func (c *Client) Add(ctx context.Context, reader io.Reader, name string) (*AddResponse, error) {
	// Track original size by reading into memory first
	// This allows us to return the actual byte count, not the DAG size
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	originalSize := int64(len(data))

	// Create multipart form request for IPFS Cluster API
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Create form file field
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("failed to copy data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Add query parameters for tarball extraction
	apiURL := c.apiURL + "/add"
	if strings.HasSuffix(strings.ToLower(name), ".tar.gz") || strings.HasSuffix(strings.ToLower(name), ".tgz") {
		apiURL += "?extract=true"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create add request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("add request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("add failed with status %d: %s", resp.StatusCode, string(body))
	}

	// IPFS Cluster streams NDJSON responses. We need to drain the entire stream
	// to prevent the connection from closing prematurely, which would cancel
	// the cluster's pinning operation. Read all JSON objects and keep the last one.
	dec := json.NewDecoder(resp.Body)
	var last AddResponse
	var hasResult bool

	for {
		var chunk AddResponse
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to decode add response: %w", err)
		}
		last = chunk
		hasResult = true
	}

	if !hasResult {
		return nil, fmt.Errorf("add response missing CID")
	}

	// Ensure name is set if provided
	if last.Name == "" && name != "" {
		last.Name = name
	}

	// Override size with original byte count (not DAG size)
	last.Size = originalSize

	return &last, nil
}

// AddDirectory adds all files in a directory to IPFS and returns the root directory CID
// Uses IPFS daemon's multipart upload to preserve directory structure
func (c *Client) AddDirectory(ctx context.Context, dirPath string) (*AddResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	var totalSize int64
	var fileCount int

	// Walk directory and add all files to multipart request
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories themselves (IPFS will create them from file paths)
		if info.IsDir() {
			return nil
		}

		// Get relative path from dirPath
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Read file
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		totalSize += int64(len(data))
		fileCount++

		// Add file to multipart with relative path
		part, err := writer.CreateFormFile("file", relPath)
		if err != nil {
			return fmt.Errorf("failed to create form file: %w", err)
		}

		if _, err := part.Write(data); err != nil {
			return fmt.Errorf("failed to write file data: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if fileCount == 0 {
		return nil, fmt.Errorf("no files found in directory")
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Upload to IPFS daemon (not Cluster) with wrap-in-directory
	// This creates a UnixFS directory structure
	ipfsDaemonURL := c.ipfsAPIURL + "/api/v0/add?wrap-in-directory=true"

	req, err := http.NewRequestWithContext(ctx, "POST", ipfsDaemonURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create add request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("add request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("add failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read NDJSON responses
	// IPFS daemon returns entries for each file and subdirectory
	// The last entry should be the root directory (or deepest subdirectory if no wrapper)
	dec := json.NewDecoder(resp.Body)
	var rootCID string
	var lastEntry ipfsDaemonAddResponse

	for {
		var chunk ipfsDaemonAddResponse
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to decode add response: %w", err)
		}
		lastEntry = chunk

		// With wrap-in-directory, the entry with empty name is the wrapper directory
		if chunk.Name == "" {
			rootCID = chunk.Hash
		}
	}

	// Use the last entry if no wrapper directory found
	if rootCID == "" {
		rootCID = lastEntry.Hash
	}

	if rootCID == "" {
		return nil, fmt.Errorf("no root CID returned from IPFS daemon")
	}

	c.logger.Debug("Directory uploaded to IPFS",
		zap.String("root_cid", rootCID),
		zap.Int("file_count", fileCount),
		zap.Int64("total_size", totalSize))

	// Pin to cluster for distribution
	_, err = c.Pin(ctx, rootCID, "", 1)
	if err != nil {
		c.logger.Warn("Failed to pin directory to cluster",
			zap.String("cid", rootCID),
			zap.Error(err))
	}

	return &AddResponse{
		Cid:  rootCID,
		Size: totalSize,
	}, nil
}

// Pin pins a CID with specified replication factor
// IPFS Cluster expects pin options (including name) as query parameters, not in JSON body
func (c *Client) Pin(ctx context.Context, cid string, name string, replicationFactor int) (*PinResponse, error) {
	// Build URL with query parameters
	reqURL := c.apiURL + "/pins/" + cid
	values := url.Values{}
	values.Set("replication-min", fmt.Sprintf("%d", replicationFactor))
	values.Set("replication-max", fmt.Sprintf("%d", replicationFactor))
	if name != "" {
		values.Set("name", name)
	}
	if len(values) > 0 {
		reqURL += "?" + values.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create pin request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pin request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pin failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result PinResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode pin response: %w", err)
	}

	// If IPFS Cluster doesn't return the name in the response, use the one from the request
	if result.Name == "" && name != "" {
		result.Name = name
	}
	// Ensure CID is set
	if result.Cid == "" {
		result.Cid = cid
	}

	return &result, nil
}

// PinStatus retrieves the status of a pinned CID
func (c *Client) PinStatus(ctx context.Context, cid string) (*PinStatus, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL+"/pins/"+cid, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create pin status request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pin status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("pin not found: %s", cid)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pin status failed with status %d: %s", resp.StatusCode, string(body))
	}

	// IPFS Cluster returns GlobalPinInfo, we need to map it to our PinStatus
	var gpi struct {
		Cid     string `json:"cid"`
		Name    string `json:"name"`
		PeerMap map[string]struct {
			Status interface{} `json:"status"` // TrackerStatus can be string or int
			Error  string      `json:"error,omitempty"`
		} `json:"peer_map"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gpi); err != nil {
		return nil, fmt.Errorf("failed to decode pin status response: %w", err)
	}

	// Use name from GlobalPinInfo
	name := gpi.Name

	// Aggregate the per-peer statuses HONESTLY. IPFS-Cluster's POST /pins
	// returns before the async per-peer pinning finishes, so a CID can be
	// "pinning"/"remote"/errored on some peers while our caller needs to know
	// whether it is DURABLE everywhere before serving invokes (bugboard #137).
	// We must never optimistically report "pinned" when we can't confirm it.
	peers := make([]string, 0, len(gpi.PeerMap))
	pinnedPeers := 0
	var errorMsg string
	var firstNonPinned string
	anyError := false
	anyPinning := false
	for peerID, pinInfo := range gpi.PeerMap {
		peers = append(peers, peerID)
		s := normalizePeerStatus(pinInfo.Status)
		switch {
		case s == PinStatusPinned:
			pinnedPeers++
		case isErrorStatus(s):
			anyError = true
		case isPinningStatus(s):
			anyPinning = true
		}
		if s != PinStatusPinned && firstNonPinned == "" {
			firstNonPinned = s
		}
		if pinInfo.Error != "" {
			errorMsg = pinInfo.Error
		}
	}

	status := aggregatePinStatus(len(peers), pinnedPeers, anyError, anyPinning, firstNonPinned)

	result := &PinStatus{
		Cid:               gpi.Cid,
		Name:              name,
		Status:            status,
		ReplicationMin:    0, // Not available in GlobalPinInfo
		ReplicationMax:    0, // Not available in GlobalPinInfo
		ReplicationFactor: len(peers),
		Peers:             peers,
		PinnedPeers:       pinnedPeers,
		TotalPeers:        len(peers),
		Error:             errorMsg,
	}

	// Ensure CID is set
	if result.Cid == "" {
		result.Cid = cid
	}

	return result, nil
}

// normalizePeerStatus renders a per-peer TrackerStatus (which the cluster API
// encodes as either a string or a number) to a lowercase string.
func normalizePeerStatus(raw interface{}) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(fmt.Sprintf("%v", raw))
}

// isErrorStatus reports whether a per-peer status represents a pin failure.
func isErrorStatus(s string) bool {
	switch s {
	case "pin_error", "cluster_error":
		return true
	}
	return strings.Contains(s, "error")
}

// isPinningStatus reports whether a per-peer status is an in-progress pin.
func isPinningStatus(s string) bool {
	switch s {
	case "pinning", "pin_queued", "queued":
		return true
	}
	return false
}

// aggregatePinStatus rolls the per-peer tallies into a single honest cluster
// status. "pinned" requires EVERY peer pinned; errors win over in-progress; an
// empty peer_map is "unknown" (the cluster couldn't confirm anything).
func aggregatePinStatus(totalPeers, pinnedPeers int, anyError, anyPinning bool, firstNonPinned string) string {
	switch {
	case totalPeers == 0:
		return PinStatusUnknown
	case pinnedPeers == totalPeers:
		return PinStatusPinned
	case anyError:
		return PinStatusError
	case anyPinning:
		return PinStatusPinning
	default:
		return firstNonPinned
	}
}

// Unpin removes a pin from a CID
func (c *Client) Unpin(ctx context.Context, cid string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.apiURL+"/pins/"+cid, nil)
	if err != nil {
		return fmt.Errorf("failed to create unpin request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unpin request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unpin failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Get retrieves content from IPFS by CID
// Note: This uses the IPFS HTTP API (typically on port 5001), not the Cluster API
func (c *Client) Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error) {
	// Use the client's configured IPFS API URL if not provided
	if ipfsAPIURL == "" {
		ipfsAPIURL = c.ipfsAPIURL
	}

	url := fmt.Sprintf("%s/api/v0/cat?arg=%s", ipfsAPIURL, cid)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("content not found (CID: %s). The content may not be available on the IPFS node, or the IPFS API may not be accessible at %s", cid, ipfsAPIURL)
		}
		return nil, fmt.Errorf("get failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// EvictLocal immediately reclaims the blocks of a CID from THIS node's local
// kubo blockstore, so an unpinned CID becomes unfetchable without waiting for
// the scheduled `ipfs repo gc` sweep (bugboard #153 — the ~6h privacy window).
//
// It walks the CID's DAG via `refs -r` and `block rm`s each block (plus the
// root) WITHOUT --force. block rm without force is pin-safe: kubo refuses to
// remove a block still part of any pinned DAG, so a block shared with another
// still-pinned CID is left intact — defence in depth alongside the caller's
// cross-namespace zero-pin gate. Uses c.ipfsAPIURL (the kubo daemon API), NOT
// the cluster API.
//
// Best-effort and idempotent: a block already absent is a no-op success; a
// block still locally pinned (cluster unpin not yet propagated to this node) is
// surfaced in the joined error so the caller can retry. Returns blocks removed.
func (c *Client) EvictLocal(ctx context.Context, cid string) (int, error) {
	blocks, err := c.dagBlocks(ctx, cid)
	if err != nil {
		return 0, fmt.Errorf("enumerate blocks for %s: %w", cid, err)
	}
	// refs -r returns descendants only; the root block must be removed too.
	blocks = append(blocks, cid)

	removed := 0
	var errs []error
	for _, block := range blocks {
		if err := c.blockRm(ctx, block); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// dagBlocks returns the unique recursive refs (DAG descendants) of a CID from
// the local kubo daemon.
func (c *Client) dagBlocks(ctx context.Context, cid string) ([]string, error) {
	q := url.Values{}
	q.Set("arg", cid)
	q.Set("recursive", "true")
	q.Set("unique", "true")
	req, err := http.NewRequestWithContext(ctx, "POST", c.ipfsAPIURL+"/api/v0/refs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refs failed with status %d: %s", resp.StatusCode, string(body))
	}
	var refs []string
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var line struct {
			Ref string `json:"Ref"`
			Err string `json:"Err"`
		}
		if err := dec.Decode(&line); err != nil {
			return nil, fmt.Errorf("decode refs stream: %w", err)
		}
		if line.Err != "" {
			return nil, fmt.Errorf("refs traversal error: %s", line.Err)
		}
		if line.Ref != "" {
			refs = append(refs, line.Ref)
		}
	}
	return refs, nil
}

// blockRm removes a single block from the local kubo blockstore WITHOUT force.
// An absent block is an idempotent success; a still-pinned block is surfaced as
// an error (retryable once cluster unpin has propagated to this node).
func (c *Client) blockRm(ctx context.Context, block string) error {
	q := url.Values{}
	q.Set("arg", block)
	// force stays false — pin-safety relies on kubo refusing pinned blocks.
	req, err := http.NewRequestWithContext(ctx, "POST", c.ipfsAPIURL+"/api/v0/block/rm?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("block rm %s failed with status %d: %s", block, resp.StatusCode, string(body))
	}
	// block/rm streams one JSON object per arg: {"Hash":"...","Error":"..."}.
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var line struct {
			Hash  string `json:"Hash"`
			Error string `json:"Error"`
		}
		if err := dec.Decode(&line); err != nil {
			return fmt.Errorf("decode block rm stream: %w", err)
		}
		if line.Error == "" {
			continue
		}
		low := strings.ToLower(line.Error)
		// Already gone → reclaim goal already met (idempotent success).
		if strings.Contains(low, "not found") || strings.Contains(low, "not exist") || strings.Contains(low, "no such") {
			continue
		}
		return fmt.Errorf("block rm %s: %s", block, line.Error)
	}
	return nil
}

// Close closes the IPFS client connection
func (c *Client) Close(ctx context.Context) error {
	// HTTP client doesn't need explicit closing
	return nil
}
