// Package fleet describes the servers one e2e run created and lets tests reach
// them. The provisioner writes a State file; every feature test package reads it.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
)

// Node roles, matching the roles the orama CLI installs.
const (
	RoleNameserver = "nameserver"
	RoleNode       = "node"
)

// Node is one server in the run.
type Node struct {
	// Name is the stable label used in reports: node-1, node-2, node-3, extra-1, probe-1.
	Name string `json:"name"`
	// Role is RoleNameserver or RoleNode.
	Role string `json:"role"`
	// PublicIP is the address the internet sees; it is what SSH and DNS glue use.
	PublicIP string `json:"public_ip"`
	// WGIP is the overlay address (10.0.0.x) once the node has joined; empty before.
	WGIP string `json:"wg_ip,omitempty"`
	// SSHUser is the login the harness uses (root on Hetzner images).
	SSHUser string `json:"ssh_user"`
	// ServerID is the Hetzner server id, used by teardown and the orphan sweep.
	ServerID int64 `json:"server_id"`
	// Location is the Hetzner location (nbg1, hel1, ash, ...).
	Location string `json:"location"`
}

// State is everything a test needs to know about the run. It is written once by
// the provisioner and re-written when stages add or remove nodes.
type State struct {
	// RunID is unique per run and is the value of the Hetzner label e2e-run.
	RunID string `json:"run_id"`
	// Env is the name of the orama environment the CLI was pointed at (orama env add).
	Env string `json:"env"`
	// BaseDomain is the per-run subdomain, e.g. e2e-ab12cd.dbrsteting.bid.
	BaseDomain string `json:"base_domain"`
	// GatewayURL is the public gateway, https://<BaseDomain>.
	GatewayURL string `json:"gateway_url"`
	// CAFile is a PEM bundle with the Let's Encrypt staging roots; every HTTP client pins it.
	CAFile string `json:"ca_file"`

	// Nodes are the three core servers; Extras are on-demand servers (fourth
	// node, restore target); Probes are remote vantage points in other locations.
	Nodes  []Node `json:"nodes"`
	Extras []Node `json:"extras,omitempty"`
	Probes []Node `json:"probes,omitempty"`

	// OramaBin is the CLI under test; Home is the isolated HOME it runs with.
	OramaBin string `json:"orama_bin"`
	Home     string `json:"home"`
	// PreviousOramaBin is the CLI of the previous release, for upgrade tests; empty when none was built.
	PreviousOramaBin string `json:"previous_orama_bin,omitempty"`
	// ArchivePath and PreviousArchivePath are the signed archives of HEAD and of the previous release.
	ArchivePath         string `json:"archive_path"`
	PreviousArchivePath string `json:"previous_archive_path,omitempty"`

	// RWSock is the socket of the throwaway RootWallet agent the CLI signs through.
	RWSock string `json:"rw_sock"`
	// OperatorAddress is the EVM address of the throwaway operator wallet.
	OperatorAddress string `json:"operator_address"`

	// SSHKeyFile is the per-run private key the harness uses to reach nodes directly.
	SSHKeyFile string `json:"ssh_key_file"`
	// KnownHostsFile pins the host keys the provisioner recorded.
	KnownHostsFile string `json:"known_hosts_file"`

	// ChainID is the chain the co-hosted validators run, always containing -devnet-.
	ChainID string `json:"chain_id"`
	// ChainRPC is the RPC endpoint reachable from the runner through an SSH tunnel or the gateway proxy.
	ChainRPC string `json:"chain_rpc,omitempty"`

	// ArtifactDir receives logs, reports and evidence for the run.
	ArtifactDir string `json:"artifact_dir"`
}

// Load reads a state file.
func Load(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read fleet state %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("failed to parse fleet state %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the state file with mode 0600.
func (s *State) Save(path string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode fleet state: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("failed to write fleet state %s: %w", path, err)
	}
	return nil
}
