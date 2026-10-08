// Package orama holds the Caddy modules an Orama node's Caddy is built with:
//
//   - dns.providers.orama answers ACME DNS-01 challenges through the index
//     gateway's /v1/internal/acme, which publishes the TXT record in the
//     cluster's own DNS.
//   - caddy.storage.orama keeps Caddy's certificates, ACME account and locks in
//     the cluster's shared store, through the index gateway's
//     /v1/internal/tls-store, so a certificate is obtained once per cluster and
//     every node serves it.
//
// This is its own Go module: xcaddy builds it into Caddy, and it cannot import
// the repository's core packages. The wire formats it shares with them — the
// MACs, the key derivation and the sealing of stored values — are spelled out
// here and in core/pkg/auth and core/pkg/tlsstore, and both sides carry the
// same test vectors.
package orama
