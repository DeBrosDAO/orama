package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// legacyTURNHostPrefix is the label prefix of the legacy two-label plain-TURN
// host "turn.ns-<namespace>.<baseDomain>" (used for UDP/TCP TURN on :3478).
const legacyTURNHostPrefix = "turn.ns-"

// stealthHostHashBytes is how many bytes of the namespace digest appear in the
// stealth hostname label. 6 bytes (12 hex chars) keeps the label CDN-bland
// while making cross-namespace collisions negligible at platform scale.
const stealthHostHashBytes = 6

// StealthHostForNamespace derives the censorship-resistant TURNS hostname for
// a namespace: "cdn-<12-hex-of-sha256(namespace)>.<baseDomain>".
//
// Design (feat-124): the label must NOT contain the namespace (an SNI string
// like "cdn.ns-anchat-test.…" hands DPI the exact app to block), must be
// deterministic so every component (cluster manager, namespace gateway, SNI
// router, DNS) derives the same value with no extra coordination, and must be
// unique per namespace because the SNI router maps it to that namespace's
// TURN-TLS backend.
func StealthHostForNamespace(namespace, baseDomain string) string {
	sum := sha256.Sum256([]byte(namespace))
	return fmt.Sprintf("cdn-%s.%s", hex.EncodeToString(sum[:stealthHostHashBytes]), baseDomain)
}

// TLSHostForNamespace derives the TURNS (TLS) hostname a client uses for the
// `turns:…:5349` URI: "turn-<namespace>.<baseDomain>".
//
// It is a SINGLE-label subdomain of the base domain on purpose. The legacy
// hostname "turn.ns-<namespace>.<baseDomain>" is TWO labels below the base, so
// the `*.<baseDomain>` wildcard certificate Caddy already provisions for HTTPS
// does NOT cover it — and the per-domain ACME provisioning path can't run
// (orama-node is ProtectSystem=strict and cannot write /etc/caddy), so that host
// was permanently stuck on a browser-rejected self-signed cert. A single-label
// host IS covered by the wildcard, so the TURN server can present a CA-valid
// cert for it with no per-namespace provisioning — mirroring the stealth host.
// Plain UDP/TCP TURN (port 3478) needs no cert and keeps using the legacy host.
//
// This yields a single-label host ONLY because namespace names are validated to
// [a-z0-9-] (no dots) upstream. If that invariant is ever loosened, a namespace
// containing a dot would produce a multi-label host the wildcard can't cover —
// silently reintroducing the self-signed-cert bug this fix removes.
func TLSHostForNamespace(namespace, baseDomain string) string {
	return fmt.Sprintf("turn-%s.%s", namespace, baseDomain)
}

// TLSHostFromLegacyTURNHost converts the legacy two-label plain-TURN host
// "turn.ns-<ns>.<base>" into the single-label TLS host "turn-<ns>.<base>". It
// derives the TURNS URI host from the exact value already used for UDP/TCP, so
// their base domains can never drift. Returns "" when host lacks the expected
// prefix (TURN disabled / unexpected shape) — the caller then leaves the TURNS
// host unset and falls back to the legacy host.
func TLSHostFromLegacyTURNHost(legacyHost string) string {
	if !strings.HasPrefix(legacyHost, legacyTURNHostPrefix) {
		return ""
	}
	return "turn-" + strings.TrimPrefix(legacyHost, legacyTURNHostPrefix)
}
