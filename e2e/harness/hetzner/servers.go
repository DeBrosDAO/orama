package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const (
	// LabelRun names the run a resource belongs to; its value is the run id.
	LabelRun = "e2e-run"
	// LabelTTL is how long a resource may live, as a Go duration (e.g. 6h);
	// the orphan sweep deletes anything older.
	LabelTTL = "e2e-ttl"
	// perPage is the page size of every list call.
	perPage = 50
	// statusRunning is a server that has booted.
	statusRunning = "running"
	// maxUserDataBytes is the API's limit on a server's cloud-init user data.
	maxUserDataBytes = 32 << 10
)

// Server is a Hetzner server as the harness uses it.
type Server struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Created   time.Time         `json:"created"`
	Labels    map[string]string `json:"labels"`
	PublicNet struct {
		IPv4 struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
	} `json:"public_net"`
	Datacenter struct {
		Location struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"datacenter"`
}

// IPv4 is the server's public address, empty until it has one.
func (s *Server) IPv4() string { return s.PublicNet.IPv4.IP }

// CreateServerOpts describes a server to create.
type CreateServerOpts struct {
	Name       string
	ServerType string
	Image      string
	Location   string
	SSHKeyID   int64
	FirewallID int64
	Labels     map[string]string
	// UserData is cloud-init configuration run at first boot (at most 32
	// KiB). It is readable by anyone with the project token and by any
	// process on the server through the metadata service.
	UserData string
}

type createServerBody struct {
	Name       string            `json:"name"`
	ServerType string            `json:"server_type"`
	Image      string            `json:"image"`
	Location   string            `json:"location"`
	SSHKeys    []int64           `json:"ssh_keys"`
	Labels     map[string]string `json:"labels"`
	Firewalls  []firewallRef     `json:"firewalls,omitempty"`
	Start      bool              `json:"start_after_create"`
	UserData   string            `json:"user_data,omitempty"`
}

type firewallRef struct {
	Firewall int64 `json:"firewall"`
}

// CreateServer creates and starts a server.
func (c *Client) CreateServer(ctx context.Context, o CreateServerOpts) (*Server, error) {
	if len(o.UserData) > maxUserDataBytes {
		return nil, fmt.Errorf("server %s: user data is %d bytes, over the API's %d", o.Name, len(o.UserData), maxUserDataBytes)
	}
	body := createServerBody{
		Name: o.Name, ServerType: o.ServerType, Image: o.Image, Location: o.Location,
		SSHKeys: []int64{o.SSHKeyID}, Labels: o.Labels, Start: true, UserData: o.UserData,
	}
	if o.FirewallID != 0 {
		body.Firewalls = []firewallRef{{Firewall: o.FirewallID}}
	}
	var resp struct {
		Server Server `json:"server"`
	}
	if err := c.do(ctx, "POST", "/servers", body, &resp); err != nil {
		return nil, fmt.Errorf("failed to create server %s: %w", o.Name, err)
	}
	return &resp.Server, nil
}

// GetServer reads one server.
func (c *Client) GetServer(ctx context.Context, id int64) (*Server, error) {
	var resp struct {
		Server Server `json:"server"`
	}
	if err := c.do(ctx, "GET", "/servers/"+strconv.FormatInt(id, 10), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Server, nil
}

// ListServers returns every server matching selector (a Hetzner label
// selector); an empty selector lists the whole project.
func (c *Client) ListServers(ctx context.Context, selector string) ([]Server, error) {
	var all []Server
	err := c.list(ctx, "/servers", selector, "servers", func(raw json.RawMessage) error {
		var page []Server
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// DeleteServer deletes a server at once (no shutdown). A server that is
// already gone is not an error.
func (c *Client) DeleteServer(ctx context.Context, id int64) error {
	err := c.do(ctx, "DELETE", "/servers/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("failed to delete server %d: %w", id, err)
	}
	return nil
}

// WaitRunning polls the server every interval until it is running with a
// public IPv4, or ctx ends.
func (c *Client) WaitRunning(ctx context.Context, id int64, interval time.Duration) (*Server, error) {
	var last *Server
	err := poll(ctx, interval, func() (bool, error) {
		s, err := c.GetServer(ctx, id)
		if err != nil {
			return false, err
		}
		last = s
		return s.Status == statusRunning && s.IPv4() != "", nil
	})
	if err != nil {
		status := "unknown"
		if last != nil {
			status = last.Status
		}
		return nil, fmt.Errorf("server %d is not running (last status %s): %w", id, status, err)
	}
	return last, nil
}

// WaitGone polls until the server no longer exists, or ctx ends.
func (c *Client) WaitGone(ctx context.Context, id int64, interval time.Duration) error {
	return poll(ctx, interval, func() (bool, error) {
		_, err := c.GetServer(ctx, id)
		if IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

// poll calls check every interval until it reports done, fails, or ctx ends.
func poll(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gave up waiting: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// list walks every page of a collection, handing each page's items to add.
func (c *Client) list(ctx context.Context, path, selector, key string, add func(json.RawMessage) error) error {
	for page := 1; page > 0; {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
		if selector != "" {
			q.Set("label_selector", selector)
		}
		var resp map[string]json.RawMessage
		if err := c.do(ctx, "GET", path+"?"+q.Encode(), nil, &resp); err != nil {
			return err
		}
		if err := add(resp[key]); err != nil {
			return fmt.Errorf("hetzner GET %s: failed to decode %s: %w", path, key, err)
		}
		next, err := nextPage(resp["meta"])
		if err != nil {
			return fmt.Errorf("hetzner GET %s: %w", path, err)
		}
		if next != 0 && next <= page {
			return fmt.Errorf("hetzner GET %s: page %d names next page %d, which does not advance", path, page, next)
		}
		page = next
	}
	return nil
}

// nextPage reads meta.pagination.next_page; 0 means there is none.
func nextPage(meta json.RawMessage) (int, error) {
	if len(meta) == 0 {
		return 0, nil
	}
	var m struct {
		Pagination struct {
			NextPage *int `json:"next_page"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(meta, &m); err != nil {
		return 0, fmt.Errorf("failed to decode pagination: %w", err)
	}
	if m.Pagination.NextPage == nil {
		return 0, nil
	}
	return *m.Pagination.NextPage, nil
}
