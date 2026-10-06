package monitor

import (
	"encoding/json"
	"fmt"
	"sort"
)

// NamespaceRow is one row of `orama monitor namespaces --json`: a namespace
// as one node hosting it reports it (core/cmd/orama/internal/monitor/display
// NamespacesJSON). A node that does not host the namespace has no row.
type NamespaceRow struct {
	Namespace string `json:"namespace"`
	Host      string `json:"host"`
	RQLiteUp  bool   `json:"rqlite_up"`
	OlricUp   bool   `json:"olric_up"`
	GatewayUp bool   `json:"gateway_up"`
}

// ParseNamespaceRows decodes `orama monitor namespaces --json`.
func ParseNamespaceRows(raw []byte) ([]NamespaceRow, error) {
	var rows []NamespaceRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("failed to parse the monitor namespaces rows: %w", err)
	}
	return rows, nil
}

// HostsOf returns, sorted and without repeats, the hosts that report a row
// for namespace name.
func HostsOf(rows []NamespaceRow, name string) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, r := range rows {
		if r.Namespace == name && !seen[r.Host] {
			seen[r.Host] = true
			hosts = append(hosts, r.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}
