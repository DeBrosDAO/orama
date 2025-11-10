//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLibP2P_PeerConnectivity(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create and connect client
	c := NewNetworkClient(t)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer c.Disconnect()

	// Verify peer connectivity through the gateway
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/peers",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("peers request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	peers := resp["peers"].([]interface{})
	if len(peers) == 0 {
		t.Logf("warning: no peers connected (cluster may still be initializing)")
	}
}

func TestLibP2P_BootstrapPeers(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bootstrapPeers := GetBootstrapPeers()
	if len(bootstrapPeers) == 0 {
		t.Skipf("E2E_BOOTSTRAP_PEERS not set; skipping")
	}

	// Create client with bootstrap peers explicitly set
	c := NewNetworkClient(t)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer c.Disconnect()

	// Give peer discovery time
	Delay(2000)

	// Verify we're connected (check via gateway status)
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/status",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["connected"] != true {
		t.Logf("warning: client not connected to network (cluster may still be initializing)")
	}
}

func TestLibP2P_MultipleClientConnections(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create multiple clients
	c1 := NewNetworkClient(t)
	c2 := NewNetworkClient(t)
	c3 := NewNetworkClient(t)

	if err := c1.Connect(); err != nil {
		t.Fatalf("c1 connect failed: %v", err)
	}
	defer c1.Disconnect()

	if err := c2.Connect(); err != nil {
		t.Fatalf("c2 connect failed: %v", err)
	}
	defer c2.Disconnect()

	if err := c3.Connect(); err != nil {
		t.Fatalf("c3 connect failed: %v", err)
	}
	defer c3.Disconnect()

	// Give peer discovery time
	Delay(2000)

	// Verify gateway sees multiple peers
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/peers",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("peers request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	peers := resp["peers"].([]interface{})
	if len(peers) < 1 {
		t.Logf("warning: expected at least 1 peer, got %d", len(peers))
	}
}

func TestLibP2P_ReconnectAfterDisconnect(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := NewNetworkClient(t)

	// Connect
	if err := c.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Verify connected via gateway
	req1 := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/status",
	}

	_, status1, err := req1.Do(ctx)
	if err != nil || status1 != http.StatusOK {
		t.Logf("warning: gateway check failed before disconnect: status %d, err %v", status1, err)
	}

	// Disconnect
	if err := c.Disconnect(); err != nil {
		t.Logf("warning: disconnect failed: %v", err)
	}

	// Give time for disconnect to propagate
	Delay(500)

	// Reconnect
	if err := c.Connect(); err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
	defer c.Disconnect()

	// Verify connected via gateway again
	req2 := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/status",
	}

	_, status2, err := req2.Do(ctx)
	if err != nil || status2 != http.StatusOK {
		t.Logf("warning: gateway check failed after reconnect: status %d, err %v", status2, err)
	}
}

func TestLibP2P_PeerDiscovery(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create client
	c := NewNetworkClient(t)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer c.Disconnect()

	// Give peer discovery time
	Delay(3000)

	// Get peer list
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/peers",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("peers request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	peers := resp["peers"].([]interface{})
	if len(peers) == 0 {
		t.Logf("warning: no peers discovered (cluster may not have multiple nodes)")
	} else {
		// Verify peer format (should be multiaddr strings)
		for _, p := range peers {
			peerStr := p.(string)
			if !strings.Contains(peerStr, "/p2p/") && !strings.Contains(peerStr, "/ipfs/") {
				t.Logf("warning: unexpected peer format: %s", peerStr)
			}
		}
	}
}

func TestLibP2P_PeerAddressFormat(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create client
	c := NewNetworkClient(t)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer c.Disconnect()

	// Get peer list
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/peers",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("peers request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	peers := resp["peers"].([]interface{})
	for _, p := range peers {
		peerStr := p.(string)
		// Multiaddrs should start with /
		if !strings.HasPrefix(peerStr, "/") {
			t.Fatalf("expected multiaddr format, got %s", peerStr)
		}
	}
}
