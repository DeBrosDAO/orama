package noderesolver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayURLForEnv_knownEnv(t *testing.T) {
	url, err := gatewayURLForEnv("devnet")
	if err != nil {
		t.Fatalf("gatewayURLForEnv(devnet): %v", err)
	}
	if url == "" {
		t.Error("expected non-empty gateway URL for devnet")
	}
}

func TestGatewayURLForEnv_unknownEnv(t *testing.T) {
	_, err := gatewayURLForEnv("nonexistent")
	if err == nil {
		t.Error("expected error for unknown environment")
	}
}

func TestResolveFromMockServer_happyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operator/nodes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// One header, and it carries a session rather than the key. The
		// gateway still accepts X-API-Key and is going to stop.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-API-Key") != "" {
			http.Error(w, "the credential was sent twice, in a header that is going away", http.StatusUnauthorized)
			return
		}

		env := r.URL.Query().Get("env")
		resp := map[string]interface{}{
			"nodes": []map[string]string{
				{"id": "node-1", "ip_address": "1.2.3.4", "environment": env, "role": "nameserver", "ssh_user": "root", "status": "active"},
				{"id": "node-2", "ip_address": "5.6.7.8", "environment": env, "role": "node", "ssh_user": "ubuntu", "status": "active"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	nodes, err := resolveFromNetworkWithURL(server.URL, "test-token", "devnet")
	if err != nil {
		t.Fatalf("resolveFromNetworkWithURL: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	if nodes[0].Host != "1.2.3.4" {
		t.Errorf("node 0 host = %q, want %q", nodes[0].Host, "1.2.3.4")
	}
	if nodes[0].Role != "nameserver" {
		t.Errorf("node 0 role = %q, want %q", nodes[0].Role, "nameserver")
	}
	if nodes[0].VaultTarget != "1.2.3.4/root" {
		t.Errorf("node 0 vault target = %q, want %q", nodes[0].VaultTarget, "1.2.3.4/root")
	}
	if nodes[0].Environment != "devnet" {
		t.Errorf("node 0 environment = %q, want %q", nodes[0].Environment, "devnet")
	}
	if nodes[1].User != "ubuntu" {
		t.Errorf("node 1 user = %q, want %q", nodes[1].User, "ubuntu")
	}
	if nodes[1].VaultTarget != "5.6.7.8/ubuntu" {
		t.Errorf("node 1 vault target = %q, want %q", nodes[1].VaultTarget, "5.6.7.8/ubuntu")
	}
}

func TestResolveFromMockServer_emptySSHUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"nodes": []map[string]string{
				{"id": "node-1", "ip_address": "1.2.3.4", "environment": "devnet", "role": "node", "ssh_user": "", "status": "active"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	nodes, err := resolveFromNetworkWithURL(server.URL, "key", "devnet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].User != "root" {
		t.Errorf("user = %q, want %q (default)", nodes[0].User, "root")
	}
	if nodes[0].VaultTarget != "1.2.3.4/root" {
		t.Errorf("vault target = %q, want %q", nodes[0].VaultTarget, "1.2.3.4/root")
	}
}

func TestResolveFromMockServer_unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := resolveFromNetworkWithURL(server.URL, "bad-key", "devnet")
	if err == nil {
		t.Error("expected error for unauthorized request")
	}
}

func TestResolveFromMockServer_emptyNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []interface{}{}})
	}))
	defer server.Close()

	nodes, err := resolveFromNetworkWithURL(server.URL, "key", "devnet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestResolveFromMockServer_malformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html>not json</html>`))
	}))
	defer server.Close()

	_, err := resolveFromNetworkWithURL(server.URL, "key", "devnet")
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}

func TestResolveFromMockServer_serverDown(t *testing.T) {
	_, err := resolveFromNetworkWithURL("http://127.0.0.1:1", "key", "devnet")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
