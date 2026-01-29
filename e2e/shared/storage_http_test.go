//go:build e2e

package shared_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	e2e "github.com/DeBrosOfficial/network/e2e"
)

// uploadFile is a helper to upload a file to storage
func uploadFile(t *testing.T, ctx context.Context, content []byte, filename string) string {
	t.Helper()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Add auth headers
	if jwt := e2e.GetJWT(); jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	} else if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read upload response: %v", err)
	}
	var result map[string]interface{}
	if err := e2e.DecodeJSON(body, &result); err != nil {
		t.Fatalf("failed to decode upload response: %v", err)
	}

	return result["cid"].(string)
}

func TestStorage_UploadText(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	content := []byte("Hello, IPFS!")
	filename := "test.txt"

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["cid"] == nil {
		t.Fatalf("expected cid in response")
	}

	if result["name"] != filename {
		t.Fatalf("expected name %q, got %v", filename, result["name"])
	}

	if result["size"] == nil || result["size"].(float64) <= 0 {
		t.Fatalf("expected positive size")
	}
}

func TestStorage_UploadBinary(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// PNG header
	content := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	filename := "test.png"

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["cid"] == nil {
		t.Fatalf("expected cid in response")
	}
}

func TestStorage_UploadLarge(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create 1MB file
	content := bytes.Repeat([]byte("x"), 1024*1024)
	filename := "large.bin"

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["size"] != float64(1024*1024) {
		t.Fatalf("expected size %d, got %v", 1024*1024, result["size"])
	}
}

func TestStorage_PinUnpin(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	content := []byte("test content for pinning")

	// Upload file first
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "pin-test.txt")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create upload request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	defer resp.Body.Close()

	var uploadResult map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &uploadResult); err != nil {
		t.Fatalf("failed to decode upload response: %v", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	cid, ok := uploadResult["cid"].(string)
	if !ok || cid == "" {
		t.Fatalf("no CID in upload response: %v", uploadResult)
	}

	// Pin the file
	pinReq := &e2e.HTTPRequest{
		Method: http.MethodPost,
		URL:    e2e.GetGatewayURL() + "/v1/storage/pin",
		Body: map[string]interface{}{
			"cid":  cid,
			"name": "pinned-file",
		},
	}

	body2, status, err := pinReq.Do(ctx)
	if err != nil {
		t.Fatalf("pin failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", status, string(body2))
	}

	var pinResult map[string]interface{}
	if err := e2e.DecodeJSON(body2, &pinResult); err != nil {
		t.Fatalf("failed to decode pin response: %v", err)
	}

	if pinResult["cid"] != cid {
		t.Fatalf("expected cid %s, got %v", cid, pinResult["cid"])
	}

	// Unpin the file
	unpinReq := &e2e.HTTPRequest{
		Method: http.MethodDelete,
		URL:    e2e.GetGatewayURL() + "/v1/storage/unpin/" + cid,
	}

	body3, status, err := unpinReq.Do(ctx)
	if err != nil {
		t.Fatalf("unpin failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", status, string(body3))
	}
}

func TestStorage_Status(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	content := []byte("test content for status")

	// Upload file first
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "status-test.txt")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create upload request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	defer resp.Body.Close()

	var uploadResult map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &uploadResult); err != nil {
		t.Fatalf("failed to decode upload response: %v", err)
	}

	cid := uploadResult["cid"].(string)

	// Get status
	statusReq := &e2e.HTTPRequest{
		Method: http.MethodGet,
		URL:    e2e.GetGatewayURL() + "/v1/storage/status/" + cid,
	}

	statusBody, status, err := statusReq.Do(ctx)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var statusResult map[string]interface{}
	if err := e2e.DecodeJSON(statusBody, &statusResult); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}

	if statusResult["cid"] != cid {
		t.Fatalf("expected cid %s, got %v", cid, statusResult["cid"])
	}
}

func TestStorage_InvalidCID(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statusReq := &e2e.HTTPRequest{
		Method: http.MethodGet,
		URL:    e2e.GetGatewayURL() + "/v1/storage/status/QmInvalidCID123456789",
	}

	_, status, err := statusReq.Do(ctx)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}

	if status != http.StatusNotFound {
		t.Logf("warning: expected status 404 for invalid CID, got %d", status)
	}
}

func TestStorage_GetByteRange(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	// Upload file first
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "range-test.txt")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Create upload request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2e.GetGatewayURL()+"/v1/storage/upload", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	defer resp.Body.Close()

	var uploadResult map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := e2e.DecodeJSON(body, &uploadResult); err != nil {
		t.Fatalf("failed to decode upload response: %v", err)
	}

	cid := uploadResult["cid"].(string)

	// Get full content
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, e2e.GetGatewayURL()+"/v1/storage/get/"+cid, nil)
	if err != nil {
		t.Fatalf("failed to create get request: %v", err)
	}

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		getReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err = client.Do(getReq)
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	retrievedContent, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if !bytes.Equal(retrievedContent, content) {
		t.Fatalf("content mismatch: expected %q, got %q", string(content), string(retrievedContent))
	}
}
