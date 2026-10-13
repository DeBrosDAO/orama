package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// SSHKey is a public key registered in the project.
type SSHKey struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Fingerprint string            `json:"fingerprint"`
	Created     time.Time         `json:"created"`
	Labels      map[string]string `json:"labels"`
}

// CreateSSHKey registers publicKey (an authorized_keys line) as name.
func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string, labels map[string]string) (*SSHKey, error) {
	body := map[string]any{"name": name, "public_key": publicKey, "labels": labels}
	var resp struct {
		SSHKey SSHKey `json:"ssh_key"`
	}
	if err := c.do(ctx, "POST", "/ssh_keys", body, &resp); err != nil {
		return nil, fmt.Errorf("failed to register SSH key %s: %w", name, err)
	}
	return &resp.SSHKey, nil
}

// ListSSHKeys returns the keys matching selector.
func (c *Client) ListSSHKeys(ctx context.Context, selector string) ([]SSHKey, error) {
	var all []SSHKey
	err := c.list(ctx, "/ssh_keys", selector, "ssh_keys", func(raw json.RawMessage) error {
		var page []SSHKey
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// DeleteSSHKey removes a key; one already gone is not an error.
func (c *Client) DeleteSSHKey(ctx context.Context, id int64) error {
	err := c.do(ctx, "DELETE", "/ssh_keys/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("failed to delete SSH key %d: %w", id, err)
	}
	return nil
}

// FirewallRule is one inbound rule.
type FirewallRule struct {
	Direction   string   `json:"direction"`
	Protocol    string   `json:"protocol"`
	Port        string   `json:"port,omitempty"`
	SourceIPs   []string `json:"source_ips"`
	Description string   `json:"description,omitempty"`
}

// Firewall is a project firewall.
type Firewall struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Created time.Time         `json:"created"`
	Labels  map[string]string `json:"labels"`
}

// CreateFirewall creates a firewall with rules.
func (c *Client) CreateFirewall(ctx context.Context, name string, rules []FirewallRule, labels map[string]string) (*Firewall, error) {
	body := map[string]any{"name": name, "rules": rules, "labels": labels}
	var resp struct {
		Firewall Firewall `json:"firewall"`
	}
	if err := c.do(ctx, "POST", "/firewalls", body, &resp); err != nil {
		return nil, fmt.Errorf("failed to create firewall %s: %w", name, err)
	}
	return &resp.Firewall, nil
}

// ListFirewalls returns the firewalls matching selector.
func (c *Client) ListFirewalls(ctx context.Context, selector string) ([]Firewall, error) {
	var all []Firewall
	err := c.list(ctx, "/firewalls", selector, "firewalls", func(raw json.RawMessage) error {
		var page []Firewall
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// DeleteFirewall removes a firewall; one already gone is not an error. A
// firewall still applied to a server is refused by the API (resource_in_use):
// delete the servers first.
func (c *Client) DeleteFirewall(ctx context.Context, id int64) error {
	err := c.do(ctx, "DELETE", "/firewalls/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("failed to delete firewall %d: %w", id, err)
	}
	return nil
}
