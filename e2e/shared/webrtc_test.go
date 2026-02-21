//go:build e2e

package shared_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	e2e "github.com/DeBrosOfficial/network/e2e"
)

// turnCredentialsResponse is the expected response from the TURN credentials endpoint.
type turnCredentialsResponse struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	TTL        int      `json:"ttl"`
}

// TestWebRTC_TURNCredentials_RequiresAuth verifies that the TURN credentials endpoint
// rejects unauthenticated requests.
func TestWebRTC_TURNCredentials_RequiresAuth(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	client := e2e.NewHTTPClient(10 * time.Second)

	req, err := http.NewRequest("POST", gatewayURL+"/v1/webrtc/turn/credentials", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
}

// TestWebRTC_TURNCredentials_ValidResponse verifies that authenticated requests to the
// TURN credentials endpoint return a valid credential structure.
func TestWebRTC_TURNCredentials_ValidResponse(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	apiKey := e2e.GetAPIKey()
	if apiKey == "" {
		t.Skip("no API key configured")
	}
	client := e2e.NewHTTPClient(10 * time.Second)

	req, err := http.NewRequest("POST", gatewayURL+"/v1/webrtc/turn/credentials", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var creds turnCredentialsResponse
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(creds.URLs) == 0 {
		t.Fatal("expected at least one TURN URL")
	}
	if creds.Username == "" {
		t.Fatal("expected non-empty username")
	}
	if creds.Credential == "" {
		t.Fatal("expected non-empty credential")
	}
	if creds.TTL <= 0 {
		t.Fatalf("expected positive TTL, got %d", creds.TTL)
	}
}

// TestWebRTC_Rooms_RequiresAuth verifies that the rooms endpoint rejects unauthenticated requests.
func TestWebRTC_Rooms_RequiresAuth(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	client := e2e.NewHTTPClient(10 * time.Second)

	req, err := http.NewRequest("GET", gatewayURL+"/v1/webrtc/rooms", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
}

// TestWebRTC_Signal_RequiresAuth verifies that the signaling WebSocket rejects
// unauthenticated connections.
func TestWebRTC_Signal_RequiresAuth(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	client := e2e.NewHTTPClient(10 * time.Second)

	// Use regular HTTP GET to the signal endpoint — without auth it should return 401
	// before WebSocket upgrade
	req, err := http.NewRequest("GET", gatewayURL+"/v1/webrtc/signal?room=test-room", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestWebRTC_Rooms_CreateAndList verifies room creation and listing with proper auth.
func TestWebRTC_Rooms_CreateAndList(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	apiKey := e2e.GetAPIKey()
	if apiKey == "" {
		t.Skip("no API key configured")
	}
	client := e2e.NewHTTPClient(10 * time.Second)

	roomID := e2e.GenerateUniqueID("e2e-webrtc-room")

	// Create room
	createBody, _ := json.Marshal(map[string]string{"room_id": roomID})
	req, err := http.NewRequest("POST", gatewayURL+"/v1/webrtc/rooms", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 200/201, got %d", resp.StatusCode)
	}

	// List rooms
	req, err = http.NewRequest("GET", gatewayURL+"/v1/webrtc/rooms", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Clean up: delete room
	req, err = http.NewRequest("DELETE", gatewayURL+"/v1/webrtc/rooms?room_id="+roomID, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete room failed: %v", err)
	}
	resp2.Body.Close()
}

// TestWebRTC_PermissionsPolicy verifies the Permissions-Policy header allows camera and microphone.
func TestWebRTC_PermissionsPolicy(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	apiKey := e2e.GetAPIKey()
	if apiKey == "" {
		t.Skip("no API key configured")
	}
	client := e2e.NewHTTPClient(10 * time.Second)

	req, err := http.NewRequest("GET", gatewayURL+"/v1/webrtc/rooms", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	pp := resp.Header.Get("Permissions-Policy")
	if pp == "" {
		t.Skip("Permissions-Policy header not set")
	}

	if !strings.Contains(pp, "camera=(self)") {
		t.Errorf("Permissions-Policy missing camera=(self), got: %s", pp)
	}
	if !strings.Contains(pp, "microphone=(self)") {
		t.Errorf("Permissions-Policy missing microphone=(self), got: %s", pp)
	}
}
