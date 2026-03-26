package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// StorageClientImpl implements StorageClient using HTTP requests to the gateway
type StorageClientImpl struct {
	client *Client
}

// Upload uploads content to IPFS and pins it
func (s *StorageClientImpl) Upload(ctx context.Context, reader io.Reader, name string) (*StorageUploadResult, error) {
	if err := s.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	gatewayURL := s.getGatewayURL()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add file field
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, reader); err != nil {
		return nil, fmt.Errorf("failed to copy data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL+"/v1/storage/upload", &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	s.addAuthHeaders(req)

	// Execute request
	client := &http.Client{Timeout: 5 * time.Minute} // Large timeout for file uploads
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result StorageUploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// Pin pins an existing CID
func (s *StorageClientImpl) Pin(ctx context.Context, cid string, name string) (*StoragePinResult, error) {
	if err := s.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	gatewayURL := s.getGatewayURL()

	reqBody := map[string]interface{}{
		"cid": cid,
	}
	if name != "" {
		reqBody["name"] = name
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL+"/v1/storage/pin", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	s.addAuthHeaders(req)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pin failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result StoragePinResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// Status gets the pin status for a CID
func (s *StorageClientImpl) Status(ctx context.Context, cid string) (*StorageStatus, error) {
	if err := s.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	gatewayURL := s.getGatewayURL()

	req, err := http.NewRequestWithContext(ctx, "GET", gatewayURL+"/v1/storage/status/"+cid, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	s.addAuthHeaders(req)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result StorageStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// Get retrieves content from IPFS by CID
func (s *StorageClientImpl) Get(ctx context.Context, cid string) (io.ReadCloser, error) {
	if err := s.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	gatewayURL := s.getGatewayURL()

	req, err := http.NewRequestWithContext(ctx, "GET", gatewayURL+"/v1/storage/get/"+cid, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	s.addAuthHeaders(req)

	client := &http.Client{Timeout: 5 * time.Minute} // Large timeout for file downloads
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("get failed with status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// Unpin removes a pin from a CID
func (s *StorageClientImpl) Unpin(ctx context.Context, cid string) error {
	if err := s.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	gatewayURL := s.getGatewayURL()

	req, err := http.NewRequestWithContext(ctx, "DELETE", gatewayURL+"/v1/storage/unpin/"+cid, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	s.addAuthHeaders(req)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unpin failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// getGatewayURL returns the gateway URL from config
func (s *StorageClientImpl) getGatewayURL() string {
	return getGatewayURL(s.client)
}

// addAuthHeaders adds authentication headers to the request
func (s *StorageClientImpl) addAuthHeaders(req *http.Request) {
	addAuthHeaders(req, s.client)
}
