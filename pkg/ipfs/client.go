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
	Health(ctx context.Context) error
	GetPeerCount(ctx context.Context) (int, error)
	Close(ctx context.Context) error
}

// Client wraps an IPFS Cluster HTTP API client for storage operations
type Client struct {
	apiURL     string
	httpClient *http.Client
	logger     *zap.Logger
}

// Config holds configuration for the IPFS client
type Config struct {
	// ClusterAPIURL is the base URL for IPFS Cluster HTTP API (e.g., "http://localhost:9094")
	// If empty, defaults to "http://localhost:9094"
	ClusterAPIURL string

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
	Error             string   `json:"error,omitempty"`
}

// AddResponse represents the response from adding content to IPFS
type AddResponse struct {
	Name string `json:"name"`
	Cid  string `json:"cid"`
	Size int64  `json:"size"`
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

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	httpClient := &http.Client{
		Timeout: timeout,
	}

	return &Client{
		apiURL:     apiURL,
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
func (c *Client) AddDirectory(ctx context.Context, dirPath string) (*AddResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Walk directory and add all files to multipart request
	var totalSize int64
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path
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

		// Add file to multipart
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

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Add with wrap-in-directory to create a root directory node
	apiURL := c.apiURL + "/add?wrap-in-directory=true"

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

	// Read NDJSON responses - the last one will be the root directory
	dec := json.NewDecoder(resp.Body)
	var last AddResponse

	for {
		var chunk AddResponse
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to decode add response: %w", err)
		}
		last = chunk
	}

	if last.Cid == "" {
		return nil, fmt.Errorf("no CID returned from IPFS")
	}

	return &AddResponse{
		Cid:  last.Cid,
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

	// Extract status from peer map (use first peer's status, or aggregate)
	status := "unknown"
	peers := make([]string, 0, len(gpi.PeerMap))
	var errorMsg string
	for peerID, pinInfo := range gpi.PeerMap {
		peers = append(peers, peerID)
		if pinInfo.Status != nil {
			// Convert status to string
			if s, ok := pinInfo.Status.(string); ok {
				if status == "unknown" || s != "" {
					status = s
				}
			} else if status == "unknown" {
				// If status is not a string, try to convert it
				status = fmt.Sprintf("%v", pinInfo.Status)
			}
		}
		if pinInfo.Error != "" {
			errorMsg = pinInfo.Error
		}
	}

	// Normalize status string (common IPFS Cluster statuses)
	if status == "" || status == "unknown" {
		status = "pinned" // Default to pinned if we have peers
		if len(peers) == 0 {
			status = "unknown"
		}
	}

	result := &PinStatus{
		Cid:               gpi.Cid,
		Name:              name,
		Status:            status,
		ReplicationMin:    0, // Not available in GlobalPinInfo
		ReplicationMax:    0, // Not available in GlobalPinInfo
		ReplicationFactor: len(peers),
		Peers:             peers,
		Error:             errorMsg,
	}

	// Ensure CID is set
	if result.Cid == "" {
		result.Cid = cid
	}

	return result, nil
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
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:5001"
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

// Close closes the IPFS client connection
func (c *Client) Close(ctx context.Context) error {
	// HTTP client doesn't need explicit closing
	return nil
}
