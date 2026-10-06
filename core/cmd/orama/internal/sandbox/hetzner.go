package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const hetznerBaseURL = "https://api.hetzner.cloud/v1"

// HetznerClient is a minimal Hetzner Cloud API client.
type HetznerClient struct {
	token      string
	httpClient *http.Client
}

// NewHetznerClient creates a new Hetzner API client.
func NewHetznerClient(token string) *HetznerClient {
	return &HetznerClient{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// --- Request helpers ---

func (c *HetznerClient) doRequest(method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, hetznerBaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

func (c *HetznerClient) get(path string) ([]byte, error) {
	body, status, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, parseHetznerError(body, status)
	}
	return body, nil
}

func (c *HetznerClient) post(path string, payload interface{}) ([]byte, error) {
	body, status, err := c.doRequest("POST", path, payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, parseHetznerError(body, status)
	}
	return body, nil
}

func (c *HetznerClient) delete(path string) error {
	_, status, err := c.doRequest("DELETE", path, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("delete %s: HTTP %d", path, status)
	}
	return nil
}

// --- API types ---

// HetznerServer represents a Hetzner Cloud server.
type HetznerServer struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"` // initializing, running, off, ...
	PublicNet  HetznerPublicNet  `json:"public_net"`
	Labels     map[string]string `json:"labels"`
	ServerType struct {
		Name string `json:"name"`
	} `json:"server_type"`
}

// HetznerPublicNet holds public networking info for a server.
type HetznerPublicNet struct {
	IPv4 struct {
		IP string `json:"ip"`
	} `json:"ipv4"`
}

// HetznerFloatingIP represents a Hetzner floating IP.
type HetznerFloatingIP struct {
	ID           int64             `json:"id"`
	IP           string            `json:"ip"`
	Server       *int64            `json:"server"` // nil if unassigned
	Labels       map[string]string `json:"labels"`
	Description  string            `json:"description"`
	HomeLocation struct {
		Name string `json:"name"`
	} `json:"home_location"`
}

// HetznerSSHKey represents a Hetzner SSH key.
type HetznerSSHKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
}

// HetznerFirewall represents a Hetzner firewall.
type HetznerFirewall struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Rules  []HetznerFWRule   `json:"rules"`
	Labels map[string]string `json:"labels"`
}

// HetznerFWRule represents a firewall rule.
type HetznerFWRule struct {
	Direction   string   `json:"direction"`
	Protocol    string   `json:"protocol"`
	Port        string   `json:"port"`
	SourceIPs   []string `json:"source_ips"`
	Description string   `json:"description,omitempty"`
}

// HetznerError represents an API error response.
type HetznerError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseHetznerError(body []byte, status int) error {
	var he HetznerError
	if err := json.Unmarshal(body, &he); err == nil && he.Error.Message != "" {
		return fmt.Errorf("hetzner API error (HTTP %d): %s — %s", status, he.Error.Code, he.Error.Message)
	}
	return fmt.Errorf("hetzner API error: HTTP %d", status)
}

// --- Server operations ---

// CreateServerRequest holds parameters for server creation.
type CreateServerRequest struct {
	Name       string            `json:"name"`
	ServerType string            `json:"server_type"`
	Image      string            `json:"image"`
	Location   string            `json:"location"`
	SSHKeys    []int64           `json:"ssh_keys"`
	Labels     map[string]string `json:"labels"`
	Firewalls  []struct {
		Firewall int64 `json:"firewall"`
	} `json:"firewalls,omitempty"`
}

// CreateServer creates a new server and returns it.
func (c *HetznerClient) CreateServer(req CreateServerRequest) (*HetznerServer, error) {
	body, err := c.post("/servers", req)
	if err != nil {
		return nil, fmt.Errorf("create server %q: %w", req.Name, err)
	}

	var resp struct {
		Server HetznerServer `json:"server"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse create server response: %w", err)
	}

	return &resp.Server, nil
}

// GetServer retrieves a server by ID.
func (c *HetznerClient) GetServer(id int64) (*HetznerServer, error) {
	body, err := c.get("/servers/" + strconv.FormatInt(id, 10))
	if err != nil {
		return nil, fmt.Errorf("get server %d: %w", id, err)
	}

	var resp struct {
		Server HetznerServer `json:"server"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse server response: %w", err)
	}

	return &resp.Server, nil
}

// DeleteServer deletes a server by ID.
func (c *HetznerClient) DeleteServer(id int64) error {
	return c.delete("/servers/" + strconv.FormatInt(id, 10))
}

// ListServersByLabel lists servers filtered by a label selector.
func (c *HetznerClient) ListServersByLabel(selector string) ([]HetznerServer, error) {
	body, err := c.get("/servers?label_selector=" + selector)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}

	var resp struct {
		Servers []HetznerServer `json:"servers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse servers response: %w", err)
	}

	return resp.Servers, nil
}

// WaitForServer polls until the server reaches "running" status.
func (c *HetznerClient) WaitForServer(id int64, timeout time.Duration) (*HetznerServer, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		srv, err := c.GetServer(id)
		if err != nil {
			return nil, err
		}
		if srv.Status == "running" {
			return srv, nil
		}
		time.Sleep(3 * time.Second)
	}
	return nil, fmt.Errorf("server %d did not reach running state within %s", id, timeout)
}

// --- Floating IP operations ---

// CreateFloatingIP creates a new floating IP.
func (c *HetznerClient) CreateFloatingIP(location, description string, labels map[string]string) (*HetznerFloatingIP, error) {
	payload := map[string]interface{}{
		"type":          "ipv4",
		"home_location": location,
		"description":   description,
		"labels":        labels,
	}

	body, err := c.post("/floating_ips", payload)
	if err != nil {
		return nil, fmt.Errorf("create floating IP: %w", err)
	}

	var resp struct {
		FloatingIP HetznerFloatingIP `json:"floating_ip"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse floating IP response: %w", err)
	}

	return &resp.FloatingIP, nil
}

// ListFloatingIPsByLabel lists floating IPs filtered by label.
func (c *HetznerClient) ListFloatingIPsByLabel(selector string) ([]HetznerFloatingIP, error) {
	body, err := c.get("/floating_ips?label_selector=" + selector)
	if err != nil {
		return nil, fmt.Errorf("list floating IPs: %w", err)
	}

	var resp struct {
		FloatingIPs []HetznerFloatingIP `json:"floating_ips"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse floating IPs response: %w", err)
	}

	return resp.FloatingIPs, nil
}

// AssignFloatingIP assigns a floating IP to a server.
func (c *HetznerClient) AssignFloatingIP(floatingIPID, serverID int64) error {
	payload := map[string]int64{"server": serverID}
	_, err := c.post("/floating_ips/"+strconv.FormatInt(floatingIPID, 10)+"/actions/assign", payload)
	if err != nil {
		return fmt.Errorf("assign floating IP %d to server %d: %w", floatingIPID, serverID, err)
	}
	return nil
}

// UnassignFloatingIP removes a floating IP assignment.
func (c *HetznerClient) UnassignFloatingIP(floatingIPID int64) error {
	_, err := c.post("/floating_ips/"+strconv.FormatInt(floatingIPID, 10)+"/actions/unassign", struct{}{})
	if err != nil {
		return fmt.Errorf("unassign floating IP %d: %w", floatingIPID, err)
	}
	return nil
}

// --- SSH Key operations ---

// UploadSSHKey uploads a public key to Hetzner.
func (c *HetznerClient) UploadSSHKey(name, publicKey string) (*HetznerSSHKey, error) {
	payload := map[string]string{
		"name":       name,
		"public_key": publicKey,
	}

	body, err := c.post("/ssh_keys", payload)
	if err != nil {
		return nil, fmt.Errorf("upload SSH key: %w", err)
	}

	var resp struct {
		SSHKey HetznerSSHKey `json:"ssh_key"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse SSH key response: %w", err)
	}

	return &resp.SSHKey, nil
}

// ListSSHKeysByFingerprint finds SSH keys matching a fingerprint.
func (c *HetznerClient) ListSSHKeysByFingerprint(fingerprint string) ([]HetznerSSHKey, error) {
	path := "/ssh_keys"
	if fingerprint != "" {
		path += "?fingerprint=" + fingerprint
	}
	body, err := c.get(path)
	if err != nil {
		return nil, fmt.Errorf("list SSH keys: %w", err)
	}

	var resp struct {
		SSHKeys []HetznerSSHKey `json:"ssh_keys"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse SSH keys response: %w", err)
	}

	return resp.SSHKeys, nil
}

// GetSSHKey retrieves an SSH key by ID.
func (c *HetznerClient) GetSSHKey(id int64) (*HetznerSSHKey, error) {
	body, err := c.get("/ssh_keys/" + strconv.FormatInt(id, 10))
	if err != nil {
		return nil, fmt.Errorf("get SSH key %d: %w", id, err)
	}

	var resp struct {
		SSHKey HetznerSSHKey `json:"ssh_key"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse SSH key response: %w", err)
	}

	return &resp.SSHKey, nil
}

// --- Firewall operations ---

// CreateFirewall creates a firewall with the given rules.
func (c *HetznerClient) CreateFirewall(name string, rules []HetznerFWRule, labels map[string]string) (*HetznerFirewall, error) {
	payload := map[string]interface{}{
		"name":   name,
		"rules":  rules,
		"labels": labels,
	}

	body, err := c.post("/firewalls", payload)
	if err != nil {
		return nil, fmt.Errorf("create firewall: %w", err)
	}

	var resp struct {
		Firewall HetznerFirewall `json:"firewall"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse firewall response: %w", err)
	}

	return &resp.Firewall, nil
}

// ListFirewallsByLabel lists firewalls filtered by label.
func (c *HetznerClient) ListFirewallsByLabel(selector string) ([]HetznerFirewall, error) {
	body, err := c.get("/firewalls?label_selector=" + selector)
	if err != nil {
		return nil, fmt.Errorf("list firewalls: %w", err)
	}

	var resp struct {
		Firewalls []HetznerFirewall `json:"firewalls"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse firewalls response: %w", err)
	}

	return resp.Firewalls, nil
}

// DeleteFirewall deletes a firewall by ID.
func (c *HetznerClient) DeleteFirewall(id int64) error {
	return c.delete("/firewalls/" + strconv.FormatInt(id, 10))
}

// DeleteFloatingIP deletes a floating IP by ID.
func (c *HetznerClient) DeleteFloatingIP(id int64) error {
	return c.delete("/floating_ips/" + strconv.FormatInt(id, 10))
}

// DeleteSSHKey deletes an SSH key by ID.
func (c *HetznerClient) DeleteSSHKey(id int64) error {
	return c.delete("/ssh_keys/" + strconv.FormatInt(id, 10))
}

// --- Location & Server Type operations ---

// HetznerLocation represents a Hetzner datacenter location.
type HetznerLocation struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`        // e.g., "fsn1", "nbg1", "hel1"
	Description string `json:"description"` // e.g., "Falkenstein DC Park 1"
	City        string `json:"city"`
	Country     string `json:"country"` // ISO 3166-1 alpha-2
}

// HetznerServerType represents a Hetzner server type with pricing.
type HetznerServerType struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`        // e.g., "cx22", "cx23"
	Description  string  `json:"description"` // e.g., "CX23"
	Cores        int     `json:"cores"`
	Memory       float64 `json:"memory"` // GB
	Disk         int     `json:"disk"`   // GB
	Architecture string  `json:"architecture"`
	Deprecation  *struct {
		Announced        string `json:"announced"`
		UnavailableAfter string `json:"unavailable_after"`
	} `json:"deprecation"` // nil = not deprecated
	Prices []struct {
		Location string `json:"location"`
		Hourly   struct {
			Gross string `json:"gross"`
		} `json:"price_hourly"`
		Monthly struct {
			Gross string `json:"gross"`
		} `json:"price_monthly"`
	} `json:"prices"`
}

// ListLocations returns all available Hetzner datacenter locations.
func (c *HetznerClient) ListLocations() ([]HetznerLocation, error) {
	body, err := c.get("/locations")
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}

	var resp struct {
		Locations []HetznerLocation `json:"locations"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse locations response: %w", err)
	}

	return resp.Locations, nil
}

// ListServerTypes returns all available server types.
func (c *HetznerClient) ListServerTypes() ([]HetznerServerType, error) {
	body, err := c.get("/server_types?per_page=50")
	if err != nil {
		return nil, fmt.Errorf("list server types: %w", err)
	}

	var resp struct {
		ServerTypes []HetznerServerType `json:"server_types"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse server types response: %w", err)
	}

	return resp.ServerTypes, nil
}

// --- Validation ---

// ValidateToken checks if the API token is valid by making a simple request.
func (c *HetznerClient) ValidateToken() error {
	_, err := c.get("/servers?per_page=1")
	if err != nil {
		return fmt.Errorf("invalid Hetzner API token: %w", err)
	}
	return nil
}

// --- Sandbox firewall rules ---

// SandboxFirewallRules returns the standard firewall rules for sandbox nodes.
func SandboxFirewallRules() []HetznerFWRule {
	allIPv4 := []string{"0.0.0.0/0"}
	allIPv6 := []string{"::/0"}
	allIPs := append(allIPv4, allIPv6...)

	return []HetznerFWRule{
		{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: allIPs, Description: "SSH"},
		{Direction: "in", Protocol: "tcp", Port: "53", SourceIPs: allIPs, Description: "DNS TCP"},
		{Direction: "in", Protocol: "udp", Port: "53", SourceIPs: allIPs, Description: "DNS UDP"},
		{Direction: "in", Protocol: "tcp", Port: "80", SourceIPs: allIPs, Description: "HTTP"},
		{Direction: "in", Protocol: "tcp", Port: "443", SourceIPs: allIPs, Description: "HTTPS"},
		{Direction: "in", Protocol: "udp", Port: "51820", SourceIPs: allIPs, Description: "WireGuard"},
	}
}
