package config

// DNSConfig is the dns block in node.yaml.
type DNSConfig struct {
	// NodeNamesZone is the zone this cluster publishes node identification names under, for
	// example stagenet.orama.network. A node of the cluster whose nameservers answer the zone reads
	// the chain's claimed names and serves <name>.<zone> as an A or AAAA record per literal IP of
	// the named node. Empty publishes none. It must be the cluster's http_gateway.base_domain or a
	// subdomain of it, and the node must have the chain reachable (a co-located global layer).
	NodeNamesZone string `yaml:"node_names_zone,omitempty"`
}
