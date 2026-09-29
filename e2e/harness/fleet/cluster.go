package fleet

// Cluster is a second, single-node Orama cluster a test installed beside the
// run's own (docs/EVAL.md): its own server, its own subdomain
// e2e-<run>-<name>.<zone> delegated to it, a Let's Encrypt staging
// certificate, and its own `orama env` entry and CA file in the run's CLI
// HOME. The run's fleet does not include it; reach its node with the
// fleet's SSH helpers through Node.
type Cluster struct {
	// Name is the label the test chose (the <name> of the subdomain).
	Name string `json:"name"`
	// Env is the orama environment that points at it (--env).
	Env string `json:"env"`
	// BaseDomain is e2e-<run>-<name>.<zone>.
	BaseDomain string `json:"base_domain"`
	// GatewayURL is https://<BaseDomain>.
	GatewayURL string `json:"gateway_url"`
	// CAFile trusts the staging roots for BaseDomain (orama env add --ca-file).
	CAFile string `json:"ca_file"`
	// Node is its one server, genesis and nameserver.
	Node Node `json:"node"`
	// HostKey is the SHA256:... fingerprint pinned for Node.
	HostKey string `json:"host_key"`
}
