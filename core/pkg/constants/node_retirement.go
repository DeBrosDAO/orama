package constants

// RetiredNodeLastSeen is the last_seen `orama node remove` gives a node it
// retires (cmd/orama/internal/production/clusterops). A retired node keeps its
// dns_nodes row, so the cluster can still find and purge its DNS records; this
// date is what says it is no longer a member. It lives here, apart from
// pkg/namespace, so the gateway's node API can refuse a retired node without
// importing the namespace manager.
const RetiredNodeLastSeen = "1970-01-01 00:00:00"
