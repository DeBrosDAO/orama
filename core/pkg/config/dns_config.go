package config

// DNSConfig is the dns block in node.yaml.
type DNSConfig struct {
	// NodeNamesZone is the zone this cluster publishes node identification names under, for
	// example nodes.stagenet.orama.network. A node of the cluster whose nameservers answer the zone
	// reads the chain's claimed names and serves <name>.<zone> as an A or AAAA record per literal IP
	// of the named node. Empty publishes none. It must be a dedicated sub-zone strictly below the
	// cluster's http_gateway.base_domain, never the base domain itself (a claimed name would sit
	// next to the hostnames the cluster publishes there), and the node must have the chain reachable
	// (a co-located global layer).
	NodeNamesZone string `yaml:"node_names_zone,omitempty"`
}
