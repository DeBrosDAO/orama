package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// DefaultProjectServerLimit is Hetzner's default server quota per project.
const DefaultProjectServerLimit = 10

// Requirements is the least a server type must offer.
type Requirements struct {
	MinCores     int
	MinMemoryGB  float64
	MinDiskGB    int
	Architecture string
}

// ServerType is a Hetzner server type.
type ServerType struct {
	Name         string  `json:"name"`
	Cores        int     `json:"cores"`
	Memory       float64 `json:"memory"`
	Disk         int     `json:"disk"`
	Architecture string  `json:"architecture"`
	Deprecation  *struct {
		UnavailableAfter string `json:"unavailable_after"`
	} `json:"deprecation"`
	Prices []struct {
		Location string `json:"location"`
	} `json:"prices"`
}

// offeredAt reports whether the type can be ordered in location.
func (t ServerType) offeredAt(location string) bool {
	for _, p := range t.Prices {
		if p.Location == location {
			return true
		}
	}
	return false
}

// ValidateLocation refuses a location the project cannot use.
func (c *Client) ValidateLocation(ctx context.Context, name string) error {
	var names []string
	err := c.list(ctx, "/locations", "", "locations", func(raw json.RawMessage) error {
		var page []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, l := range page {
			names = append(names, l.Name)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to list Hetzner locations: %w", err)
	}
	for _, n := range names {
		if n == name {
			return nil
		}
	}
	sort.Strings(names)
	return fmt.Errorf("hetzner has no location %q; use one of: %s", name, strings.Join(names, ", "))
}

// ValidateServerType refuses a type that does not exist, is deprecated, is
// not offered in location, or is smaller than req.
func (c *Client) ValidateServerType(ctx context.Context, name, location string, req Requirements) error {
	var types []ServerType
	err := c.list(ctx, "/server_types", "", "server_types", func(raw json.RawMessage) error {
		var page []ServerType
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		types = append(types, page...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to list Hetzner server types: %w", err)
	}
	for _, t := range types {
		if t.Name == name {
			return checkServerType(t, location, req, types)
		}
	}
	return fmt.Errorf("hetzner has no server type %q; types that fit in %s: %s", name, location, fitting(types, location, req))
}

func checkServerType(t ServerType, location string, req Requirements, all []ServerType) error {
	switch {
	case t.Deprecation != nil:
		return fmt.Errorf("server type %s is deprecated (unavailable after %s); use one of: %s",
			t.Name, t.Deprecation.UnavailableAfter, fitting(all, location, req))
	case !t.offeredAt(location):
		return fmt.Errorf("server type %s is not offered in %s; use one of: %s", t.Name, location, fitting(all, location, req))
	case !meets(t, req):
		return fmt.Errorf("server type %s (%d cores, %.0f GB RAM, %d GB disk, %s) is below the node minimum "+
			"(%d cores, %.0f GB RAM, %d GB disk, %s); use one of: %s", t.Name, t.Cores, t.Memory, t.Disk, t.Architecture,
			req.MinCores, req.MinMemoryGB, req.MinDiskGB, req.Architecture, fitting(all, location, req))
	}
	return nil
}

func meets(t ServerType, req Requirements) bool {
	return t.Cores >= req.MinCores && t.Memory >= req.MinMemoryGB && t.Disk >= req.MinDiskGB &&
		(req.Architecture == "" || t.Architecture == req.Architecture)
}

// fitting names the usable types, for an error message.
func fitting(all []ServerType, location string, req Requirements) string {
	var names []string
	for _, t := range all {
		if t.Deprecation == nil && t.offeredAt(location) && meets(t, req) {
			names = append(names, t.Name)
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// CheckCapacity refuses to add want servers to a project that would then hold
// more than limit, before any is created.
func (c *Client) CheckCapacity(ctx context.Context, want, limit int) error {
	servers, err := c.ListServers(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to count the project's servers: %w", err)
	}
	if len(servers)+want > limit {
		return fmt.Errorf("the Hetzner project holds %d servers and this needs %d more, over the limit of %d: "+
			"delete leftovers (e2e sweep deletes e2e-run servers past their TTL) or raise the limit",
			len(servers), want, limit)
	}
	return nil
}
