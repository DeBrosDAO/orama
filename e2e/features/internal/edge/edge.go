//go:build e2e_fleet

// Package edge holds what the network-edge and operations feature packages
// (dns-tls, gateway-middleware, monitoring, security-audit, external-vantage
// and their destructive packages) share: a DNS client that asks one
// nameserver directly, node-side HTTP probes that keep credentials on the
// node, a "condition holds for a while" wait, and the pacing handshake a
// rate-limiter flood needs before and after it spends this address's budget.
//
// Every value here is read from core and cited where it is defined, so a test
// that asserts it asserts what the code ships.
package edge

import "time"

// Ports and paths on a node (core/pkg/constants/ports.go,
// core/pkg/install/installers/caddy.go, core/pkg/privhelper/protocol.go).
const (
	// GatewayPort is the index gateway: 127.0.0.1 and the WireGuard address.
	GatewayPort = 10104
	// CaddyUnit terminates TLS on every node (core/pkg/telemetry/report/dns.go).
	CaddyUnit = "orama-namespace-caddy@index.service"
	// CoreDNSUnit answers :53 on a nameserver.
	CoreDNSUnit = "orama-namespace-coredns@nameserver.service"
	// IndexGatewayUnit is the cluster gateway.
	IndexGatewayUnit = "orama-namespace-gateway@index.service"
	// IndexRQLiteUnit is the index rqlite CoreDNS reads its zone from.
	IndexRQLiteUnit = "orama-namespace-rqlite@index.service"
	// TorUnit and NtfyUnit are the auxiliary edge units: they terminate
	// nothing for the node (core/pkg/node/index_host.go startIndexEdgeAux).
	TorUnit  = "orama-namespace-tor@index.service"
	NtfyUnit = "orama-namespace-ntfy@index.service"
	// ACMEKeyPath is the key Caddy signs DNS-01 calls with, root:orama 0640.
	ACMEKeyPath = "/etc/caddy/orama-acme.key"
	// TLSStoreKeyPath is the master key of the cluster's certificate store,
	// which Caddy's storage module reaches /v1/internal/tls-store with,
	// root:orama 0640 (core/pkg/install/installers/caddy.go).
	TLSStoreKeyPath = "/etc/caddy/orama-tls-store.key"
	// WildcardCertPath and WildcardKeyPath are the cluster's *.<base> pair the
	// cluster gateway exports from the store for TURN, orama 0600
	// (core/pkg/constants/paths.go).
	WildcardCertPath = "/opt/orama/.orama/data/tls/wildcard.crt"
	WildcardKeyPath  = "/opt/orama/.orama/data/tls/wildcard.key"
	// CaddyAdminSocket is Caddy's admin API, 0600 in a 0700 runtime directory.
	CaddyAdminSocket = "/run/orama-caddy/admin.sock"
	// PrivhelperBin and PrivhelperSock are the privileged helper's client and
	// socket; ExitRefused is its refusal status (core/pkg/privhelper).
	PrivhelperBin  = "/usr/local/bin/orama-privhelper"
	PrivhelperSock = "/run/orama-privhelper.sock"
	ExitRefused    = 126
)

// Budgets shared by the edge features.
const (
	// PollEvery paces waits on DNS, telemetry and units.
	PollEvery = 5 * time.Second
	// NegativeTTL is how long CoreDNS caches a negative answer (NXDOMAIN or NODATA)
	// (core/pkg/coredns/rqlite/cache.go NegativeTTL).
	NegativeTTL = 30 * time.Second
	// StaleTTL is the TTL of an answer served stale while the backend is down.
	StaleTTL = 30 * time.Second
	// PluginCacheTTL is the rqlite plugin's positive cache TTL, `ttl 30` in the
	// generated Corefile (core/pkg/install/installers/coredns.go).
	PluginCacheTTL = 30 * time.Second
	// SystemRecordTTL is the TTL of the base, wildcard, glue, NS and SOA
	// records (core/pkg/node/dns_registration.go, dns_nameservers.go).
	SystemRecordTTL = 300
	// NamespaceRecordTTL is the TTL of ns-<ns> and its wildcard
	// (core/pkg/namespace/dns_manager.go).
	NamespaceRecordTTL = 60
	// ReapAfter is when a silent node is marked inactive and its records
	// purged from the base round-robin (core/pkg/node/dns_registration.go
	// reapInactiveNodeDNS: two minutes), plus two 30s sweeps.
	ReapAfter = 2*time.Minute + time.Minute
)
