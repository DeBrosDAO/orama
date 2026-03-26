// Package types defines shared types used across agent packages.
package types

// Peer represents a cluster peer with vault-guardian access.
type Peer struct {
	WGIP   string `json:"wg_ip"`
	NodeID string `json:"node_id"`
}
