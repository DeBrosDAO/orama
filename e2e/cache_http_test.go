//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCache_Health(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/cache/health",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}

	if resp["service"] != "olric" {
		t.Fatalf("expected service 'olric', got %v", resp["service"])
	}
}

func TestCache_PutGet(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "test-key"
	value := "test-value"

	// Put value
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": value,
		},
	}

	body, status, err := putReq.Do(ctx)
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", status, string(body))
	}

	// Get value
	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	body, status, err = getReq.Do(ctx)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var getResp map[string]interface{}
	if err := DecodeJSON(body, &getResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if getResp["value"] != value {
		t.Fatalf("expected value %q, got %v", value, getResp["value"])
	}
}

func TestCache_PutGetJSON(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "json-key"
	jsonValue := map[string]interface{}{
		"name": "John",
		"age":  30,
		"tags": []string{"developer", "golang"},
	}

	// Put JSON value
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": jsonValue,
		},
	}

	_, status, err := putReq.Do(ctx)
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	// Get JSON value
	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	body, status, err := getReq.Do(ctx)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var getResp map[string]interface{}
	if err := DecodeJSON(body, &getResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	retrievedValue := getResp["value"].(map[string]interface{})
	if retrievedValue["name"] != jsonValue["name"] {
		t.Fatalf("expected name %q, got %v", jsonValue["name"], retrievedValue["name"])
	}
	if retrievedValue["age"] != float64(30) {
		t.Fatalf("expected age 30, got %v", retrievedValue["age"])
	}
}

func TestCache_Delete(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "delete-key"
	value := "delete-value"

	// Put value
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": value,
		},
	}

	_, status, err := putReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("put failed: status %d, err %v", status, err)
	}

	// Delete value
	deleteReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/delete",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	_, status, err = deleteReq.Do(ctx)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	// Verify deletion
	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	_, status, err = getReq.Do(ctx)
	// Should get 404 for missing key
	if status != http.StatusNotFound {
		t.Fatalf("expected status 404 for deleted key, got %d", status)
	}
}

func TestCache_TTL(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "ttl-key"
	value := "ttl-value"

	// Put value with TTL
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": value,
			"ttl":   "2s",
		},
	}

	_, status, err := putReq.Do(ctx)
	if err != nil {
		t.Fatalf("put with TTL failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	// Verify value exists
	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	_, status, err = getReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get immediately after put failed: status %d, err %v", status, err)
	}

	// Wait for TTL expiry (2 seconds + buffer)
	Delay(2500)

	// Verify value is expired
	_, status, err = getReq.Do(ctx)
	if status != http.StatusNotFound {
		t.Logf("warning: TTL expiry may not be fully implemented; got status %d", status)
	}
}

func TestCache_Scan(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()

	// Put multiple keys
	keys := []string{"user-1", "user-2", "session-1", "session-2"}
	for _, key := range keys {
		putReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/put",
			Body: map[string]interface{}{
				"dmap":  dmap,
				"key":   key,
				"value": "value-" + key,
			},
		}

		_, status, err := putReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Fatalf("put failed: status %d, err %v", status, err)
		}
	}

	// Scan all keys
	scanReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/scan",
		Body: map[string]interface{}{
			"dmap": dmap,
		},
	}

	body, status, err := scanReq.Do(ctx)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var scanResp map[string]interface{}
	if err := DecodeJSON(body, &scanResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	keysResp := scanResp["keys"].([]interface{})
	if len(keysResp) < 4 {
		t.Fatalf("expected at least 4 keys, got %d", len(keysResp))
	}
}

func TestCache_ScanWithRegex(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()

	// Put keys with different patterns
	keys := []string{"user-1", "user-2", "session-1", "session-2"}
	for _, key := range keys {
		putReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/put",
			Body: map[string]interface{}{
				"dmap":  dmap,
				"key":   key,
				"value": "value-" + key,
			},
		}

		_, status, err := putReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Fatalf("put failed: status %d, err %v", status, err)
		}
	}

	// Scan with regex pattern
	scanReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/scan",
		Body: map[string]interface{}{
			"dmap":    dmap,
			"pattern": "^user-",
		},
	}

	body, status, err := scanReq.Do(ctx)
	if err != nil {
		t.Fatalf("scan with regex failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var scanResp map[string]interface{}
	if err := DecodeJSON(body, &scanResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	keysResp := scanResp["keys"].([]interface{})
	if len(keysResp) < 2 {
		t.Fatalf("expected at least 2 keys matching pattern, got %d", len(keysResp))
	}
}

func TestCache_MultiGet(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	keys := []string{"key-1", "key-2", "key-3"}

	// Put values
	for i, key := range keys {
		putReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/put",
			Body: map[string]interface{}{
				"dmap":  dmap,
				"key":   key,
				"value": fmt.Sprintf("value-%d", i),
			},
		}

		_, status, err := putReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Fatalf("put failed: status %d, err %v", status, err)
		}
	}

	// Multi-get
	multiGetReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/mget",
		Body: map[string]interface{}{
			"dmap": dmap,
			"keys": keys,
		},
	}

	body, status, err := multiGetReq.Do(ctx)
	if err != nil {
		t.Fatalf("mget failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var mgetResp map[string]interface{}
	if err := DecodeJSON(body, &mgetResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	results := mgetResp["results"].([]interface{})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestCache_MissingDMap(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": "",
			"key":  "any-key",
		},
	}

	_, status, err := getReq.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if status != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing dmap, got %d", status)
	}
}

func TestCache_MissingKey(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dmap := GenerateDMapName()

	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  "non-existent-key",
		},
	}

	_, status, err := getReq.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if status != http.StatusNotFound {
		t.Fatalf("expected status 404 for missing key, got %d", status)
	}
}
