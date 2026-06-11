package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

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
