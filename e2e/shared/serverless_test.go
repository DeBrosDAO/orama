//go:build e2e

package shared_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"

	e2e "github.com/DeBrosOfficial/network/e2e"
)

func TestServerless_DeployAndInvoke(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	wasmPath := "../examples/functions/bin/hello.wasm"
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		t.Skip("hello.wasm not found")
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("failed to read hello.wasm: %v", err)
	}

	funcName := "e2e-hello"
	// Use namespace from environment or default to test namespace
	namespace := os.Getenv("ORAMA_NAMESPACE")
	if namespace == "" {
		namespace = "default-test-ns" // Match the namespace from LoadTestEnv()
	}

	// 1. Deploy function
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add metadata
	_ = writer.WriteField("name", funcName)
	_ = writer.WriteField("namespace", namespace)
	_ = writer.WriteField("is_public", "true") // Make function public for E2E test

	// Add WASM file
	part, err := writer.CreateFormFile("wasm", funcName+".wasm")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	part.Write(wasmBytes)
	writer.Close()

	deployReq, _ := http.NewRequestWithContext(ctx, "POST", e2e.GetGatewayURL()+"/v1/functions", &buf)
	deployReq.Header.Set("Content-Type", writer.FormDataContentType())

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		deployReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := e2e.NewHTTPClient(1 * time.Minute)
	resp, err := client.Do(deployReq)
	if err != nil {
		t.Fatalf("deploy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 2. Invoke function
	invokePayload := []byte(`{"name": "E2E Tester"}`)
	invokeReq, _ := http.NewRequestWithContext(ctx, "POST", e2e.GetGatewayURL()+"/v1/functions/"+funcName+"/invoke?namespace="+namespace, bytes.NewReader(invokePayload))
	invokeReq.Header.Set("Content-Type", "application/json")

	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		invokeReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err = client.Do(invokeReq)
	if err != nil {
		t.Fatalf("invoke request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invoke failed with status %d: %s", resp.StatusCode, string(body))
	}

	output, _ := io.ReadAll(resp.Body)
	expected := "Hello, E2E Tester!"
	if !bytes.Contains(output, []byte(expected)) {
		t.Errorf("output %q does not contain %q", string(output), expected)
	}

	// 3. List functions
	listReq, _ := http.NewRequestWithContext(ctx, "GET", e2e.GetGatewayURL()+"/v1/functions?namespace="+namespace, nil)
	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		listReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err = client.Do(listReq)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("list failed with status %d", resp.StatusCode)
	}

	// 4. Delete function
	deleteReq, _ := http.NewRequestWithContext(ctx, "DELETE", e2e.GetGatewayURL()+"/v1/functions/"+funcName+"?namespace="+namespace, nil)
	if apiKey := e2e.GetAPIKey(); apiKey != "" {
		deleteReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err = client.Do(deleteReq)
	if err != nil {
		t.Fatalf("delete request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete failed with status %d", resp.StatusCode)
	}
}
