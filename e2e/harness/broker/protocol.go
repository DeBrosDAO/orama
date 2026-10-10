// Package broker lets feature processes use the run's cloud accounts without
// holding their credentials. The runner (`e2e-fleet run` / `test`) holds
// HCLOUD_TOKEN and CF_API_TOKEN and serves a small set of operations, each
// scoped to the run, on a unix socket in the run's work dir (a 0700
// directory, the socket 0600). Feature processes get only the socket's path,
// E2E_BROKER_SOCK; their environment never carries a token (see
// stages.FeatureEnv).
//
// The operations, and what each refuses:
//
//	dns.txt.set / dns.txt.delete   a TXT record whose name is inside this run's
//	                               subdomain or one of its cluster subdomains
//	                               (e2e-<run>[-<label>].<zone>); anything else
//	dns.records.list               the run's own records only
//	extra.add / extra.remove       an extra server of the run; never a core
//	                               node or probe
//	cluster.add / cluster.remove   a single-node eval cluster of the run
//
// One request per connection, as one JSON line each way. A client that goes
// away cancels its operation.
package broker

import (
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// EnvSock is the variable that carries the socket path to feature processes.
const EnvSock = "E2E_BROKER_SOCK"

// Operations.
const (
	OpTXTSet        = "dns.txt.set"
	OpTXTDelete     = "dns.txt.delete"
	OpRecordsList   = "dns.records.list"
	OpExtraAdd      = "extra.add"
	OpExtraRemove   = "extra.remove"
	OpClusterAdd    = "cluster.add"
	OpClusterRemove = "cluster.remove"
)

// Budgets of one operation on the server side. A cluster installs a node,
// waits for its delegation and its certificate.
const (
	dnsBudget     = 2 * time.Minute
	extraBudget   = 20 * time.Minute
	clusterBudget = 45 * time.Minute
)

// maxMessageBytes bounds one request or response line.
const maxMessageBytes = 1 << 20

// Request is one operation.
type Request struct {
	Op string `json:"op"`
	// Name is the record name (dns.*), the extra's or the cluster's name.
	Name string `json:"name,omitempty"`
	// Value is the TXT value (dns.txt.*; empty on delete: every value).
	Value string `json:"value,omitempty"`
	// Location is where extra.add creates the server (empty: the run's).
	Location string `json:"location,omitempty"`
}

// Record is one DNS record of the run.
type Record struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Response is the answer to one Request. Error is set on failure, redacted.
type Response struct {
	Error   string         `json:"error,omitempty"`
	Records []Record       `json:"records,omitempty"`
	Node    *fleet.Node    `json:"node,omitempty"`
	Cluster *fleet.Cluster `json:"cluster,omitempty"`
}

// budgetOf is how long the server lets op run.
func budgetOf(op string) time.Duration {
	switch op {
	case OpExtraAdd, OpExtraRemove:
		return extraBudget
	case OpClusterAdd, OpClusterRemove:
		return clusterBudget
	}
	return dnsBudget
}
