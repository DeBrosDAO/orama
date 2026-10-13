package sandbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{"servers": []interface{}{}})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	if err := client.ValidateToken(); err != nil {
		t.Errorf("ValidateToken() error = %v, want nil", err)
	}
}

func TestValidateToken_InvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "unauthorized",
				"message": "unable to authenticate",
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "bad-token")
	if err := client.ValidateToken(); err == nil {
		t.Error("ValidateToken() expected error for invalid token")
	}
}

func TestCreateServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/servers" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		var req CreateServerRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.Name != "sbx-test-1" {
			t.Errorf("unexpected server name: %s", req.Name)
		}
		if req.ServerType != "cx22" {
			t.Errorf("unexpected server type: %s", req.ServerType)
		}

		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server": map[string]interface{}{
				"id":     12345,
				"name":   req.Name,
				"status": "initializing",
				"public_net": map[string]interface{}{
					"ipv4": map[string]string{"ip": "1.2.3.4"},
				},
				"labels":      req.Labels,
				"server_type": map[string]string{"name": "cx22"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	server, err := client.CreateServer(CreateServerRequest{
		Name:       "sbx-test-1",
		ServerType: "cx22",
		Image:      "ubuntu-24.04",
		Location:   "fsn1",
		SSHKeys:    []int64{1},
		Labels:     map[string]string{"orama-sandbox": "test"},
	})

	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if server.ID != 12345 {
		t.Errorf("server ID = %d, want 12345", server.ID)
	}
	if server.Name != "sbx-test-1" {
		t.Errorf("server name = %s, want sbx-test-1", server.Name)
	}
	if server.PublicNet.IPv4.IP != "1.2.3.4" {
		t.Errorf("server IP = %s, want 1.2.3.4", server.PublicNet.IPv4.IP)
	}
}

func TestDeleteServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/v1/servers/12345" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	if err := client.DeleteServer(12345); err != nil {
		t.Errorf("DeleteServer() error = %v", err)
	}
}

func TestListServersByLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("label_selector") != "orama-sandbox=test" {
			t.Errorf("unexpected label_selector: %s", r.URL.Query().Get("label_selector"))
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"servers": []map[string]interface{}{
				{"id": 1, "name": "sbx-test-1", "status": "running", "public_net": map[string]interface{}{"ipv4": map[string]string{"ip": "1.1.1.1"}}, "server_type": map[string]string{"name": "cx22"}},
				{"id": 2, "name": "sbx-test-2", "status": "running", "public_net": map[string]interface{}{"ipv4": map[string]string{"ip": "2.2.2.2"}}, "server_type": map[string]string{"name": "cx22"}},
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	servers, err := client.ListServersByLabel("orama-sandbox=test")
	if err != nil {
		t.Fatalf("ListServersByLabel() error = %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("got %d servers, want 2", len(servers))
	}
}

func TestWaitForServer_AlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server": map[string]interface{}{
				"id":     1,
				"name":   "test",
				"status": "running",
				"public_net": map[string]interface{}{
					"ipv4": map[string]string{"ip": "1.1.1.1"},
				},
				"server_type": map[string]string{"name": "cx22"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	server, err := client.WaitForServer(1, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForServer() error = %v", err)
	}
	if server.Status != "running" {
		t.Errorf("server status = %s, want running", server.Status)
	}
}

func TestAssignFloatingIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/floating_ips/100/actions/assign" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]int64
		json.NewDecoder(r.Body).Decode(&body)
		if body["server"] != 200 {
			t.Errorf("unexpected server ID: %d", body["server"])
		}

		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{"action": map[string]interface{}{"id": 1, "status": "running"}})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	if err := client.AssignFloatingIP(100, 200); err != nil {
		t.Errorf("AssignFloatingIP() error = %v", err)
	}
}

func TestUploadSSHKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/ssh_keys" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ssh_key": map[string]interface{}{
				"id":          42,
				"name":        "orama-sandbox",
				"fingerprint": "aa:bb:cc:dd",
				"public_key":  "ssh-ed25519 AAAA...",
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	key, err := client.UploadSSHKey("orama-sandbox", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("UploadSSHKey() error = %v", err)
	}
	if key.ID != 42 {
		t.Errorf("key ID = %d, want 42", key.ID)
	}
}

func TestCreateFirewall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/firewalls" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"firewall": map[string]interface{}{
				"id":   99,
				"name": "orama-sandbox",
			},
		})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	fw, err := client.CreateFirewall("orama-sandbox", SandboxFirewallRules(), map[string]string{"orama-sandbox": "infra"})
	if err != nil {
		t.Fatalf("CreateFirewall() error = %v", err)
	}
	if fw.ID != 99 {
		t.Errorf("firewall ID = %d, want 99", fw.ID)
	}
}

func TestSandboxFirewallRules(t *testing.T) {
	rules := SandboxFirewallRules()
	if len(rules) != 6 {
		t.Errorf("got %d rules, want 6", len(rules))
	}

	expectedPorts := map[string]bool{"22": false, "53": false, "80": false, "443": false, "51820": false}
	for _, r := range rules {
		expectedPorts[r.Port] = true
		if r.Direction != "in" {
			t.Errorf("rule %s direction = %s, want in", r.Port, r.Direction)
		}
	}
	for port, seen := range expectedPorts {
		if !seen {
			t.Errorf("missing firewall rule for port %s", port)
		}
	}
}

func TestParseHetznerError(t *testing.T) {
	body := `{"error":{"code":"uniqueness_error","message":"server name already used"}}`
	err := parseHetznerError([]byte(body), 409)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "hetzner API error (HTTP 409): uniqueness_error — server name already used"
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err.Error(), expected)
	}
}

// newTestClient creates a HetznerClient pointing at a test server.
func newTestClient(ts *httptest.Server, token string) *HetznerClient {
	client := NewHetznerClient(token)
	// Override the base URL by using a custom transport
	client.httpClient = ts.Client()
	// We need to override the base URL — wrap the transport
	origTransport := client.httpClient.Transport
	client.httpClient.Transport = &testTransport{
		base:    origTransport,
		testURL: ts.URL,
	}
	return client
}

// testTransport rewrites requests to point at the test server.
type testTransport struct {
	base    http.RoundTripper
	testURL string
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point at the test server
	req.URL.Scheme = "http"
	req.URL.Host = t.testURL[len("http://"):]
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}
