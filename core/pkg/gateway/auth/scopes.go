package auth

import (
	"fmt"
	"sort"
	"strings"
)

// API-key scope grants (bugboard #148). A key carries a SET of these, stored
// comma-separated in api_keys.scopes. Grants gate which classes of endpoint a
// key may reach; the per-endpoint policy (which grant a given route needs)
// lives in the gateway package (requiredScope). The admin grant is a wildcard
// that satisfies every requirement.
//
// The split is data-plane (safe to ship in a public client bundle) vs
// control-plane (admin — never leaves CI/dev):
//   - data-plane: invoke, storage, push, webrtc, proxy, pubsub, cache
//   - control-plane: admin (deploy, secrets, migrations, config, key mgmt, raw rqlite)
const (
	ScopeAdmin   = "admin"   // full control-plane
	ScopeInvoke  = "invoke"  // invoke functions (data access still gated by per-user JWT)
	ScopeStorage = "storage" // IPFS storage upload/pin/get
	ScopePush    = "push"    // push device registration
	ScopeWebRTC  = "webrtc"  // TURN credentials + SFU signaling
	ScopeProxy   = "proxy"   // Anyone-routed anon proxy
	ScopePubsub  = "pubsub"  // pub/sub REST
	ScopeCache   = "cache"   // Olric cache REST
)

// knownGrants is the set of every valid grant. Used to validate issuance input
// so a typo can't mint a key with a grant no route will ever satisfy.
//
// It must list every Scope* constant. ScopePubsub was missing, so
// NormalizeGrants rejected "pubsub" as unknown — in an error message that
// listed "pubsub" among the valid grants — and no key could be minted with it.
// requiredScope demands that grant for every /v1/pubsub/ path, and no profile
// includes it, so the pub/sub REST API was reachable only by an admin key or a
// wallet JWT.
var knownGrants = map[string]struct{}{
	ScopeAdmin:   {},
	ScopeInvoke:  {},
	ScopeStorage: {},
	ScopePush:    {},
	ScopeWebRTC:  {},
	ScopeProxy:   {},
	ScopePubsub:  {},
	ScopeCache:   {},
}

// AllGrants returns every valid grant, sorted. It is the list clients are told
// about, so it and knownGrants cannot drift apart.
func AllGrants() []string {
	out := make([]string, 0, len(knownGrants))
	for g := range knownGrants {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// ScopeSet is a parsed, membership-tested set of grants held by a key.
type ScopeSet map[string]struct{}

// Has reports whether the set satisfies the required grant. An empty required
// grant is always satisfied (the endpoint needs only a valid credential). The
// admin grant satisfies every requirement.
func (s ScopeSet) Has(required string) bool {
	if required == "" {
		return true
	}
	if _, ok := s[ScopeAdmin]; ok {
		return true
	}
	_, ok := s[required]
	return ok
}

// IsAdmin reports whether the set holds the admin (full control-plane) grant.
func (s ScopeSet) IsAdmin() bool {
	_, ok := s[ScopeAdmin]
	return ok
}

// Canonical renders the set as a stable, sorted, comma-separated string —
// suitable for storage in api_keys.scopes and for embedding in a JWT claim.
func (s ScopeSet) Canonical() string {
	if len(s) == 0 {
		return ""
	}
	out := make([]string, 0, len(s))
	for g := range s {
		out = append(out, g)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// ParseScopes parses a literal comma-separated grant string into a set. It does
// NOT apply the grandfather policy — an empty string yields an empty set. Use
// ScopesFromStored for the api_keys.scopes read path.
func ParseScopes(raw string) ScopeSet {
	set := ScopeSet{}
	for _, part := range strings.Split(raw, ",") {
		g := strings.ToLower(strings.TrimSpace(part))
		if g != "" {
			set[g] = struct{}{}
		}
	}
	return set
}

// ScopesFromStored converts a stored api_keys.scopes value into a ScopeSet.
//
// An empty column grants nothing. It used to mean "minted before scoping
// existed" and was read as admin, which made every key minted by a wallet login
// an admin key — GetOrCreateAPIKey wrote no scopes column at all — and undid
// the legacy-key cutover on every login. Migration 043 writes the grant those
// keys were relying on onto the rows themselves, so the inference has nothing
// left to do and an empty set is what it says it is.
func ScopesFromStored(raw string) ScopeSet {
	return ParseScopes(raw)
}

// DataPlaneScopes is the grant set an authenticated end-user (SIWE wallet JWT)
// receives: every data-plane grant, never admin. A logged-in user may use
// storage/push/webrtc/proxy/invoke; only the admin API key may touch the
// control-plane.
func DataPlaneScopes() ScopeSet {
	return ScopeSet{
		ScopeInvoke:  {},
		ScopeStorage: {},
		ScopePush:    {},
		ScopeWebRTC:  {},
		ScopeProxy:   {},
		ScopePubsub:  {},
		ScopeCache:   {},
	}
}

// ProfileGrants maps a named key profile to its grant list. Profiles are the
// ergonomic way to mint the three shipped key tiers; a raw grant list is also
// accepted by the issuance path. Returns false for an unknown profile.
func ProfileGrants(profile string) ([]string, bool) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "admin":
		return []string{ScopeAdmin}, true
	case "app-runtime", "runtime", "app":
		return []string{ScopeInvoke, ScopeStorage, ScopePush, ScopeWebRTC, ScopeProxy}, true
	case "invoke-only", "invoke":
		return []string{ScopeInvoke}, true
	}
	return nil, false
}

// NormalizeGrants validates and canonicalizes a requested grant list (either a
// profile name or explicit comma/space-separated grants) into a stable stored
// string. Returns an error naming any unknown grant so issuance fails loud
// rather than minting a key that no route can satisfy.
func NormalizeGrants(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("scope is required (a profile name or grant list)")
	}
	if grants, ok := ProfileGrants(requested); ok {
		return ScopeSet(setOf(grants)).Canonical(), nil
	}
	// Explicit grant list — accept comma OR space separated.
	fields := strings.FieldsFunc(requested, func(r rune) bool { return r == ',' || r == ' ' })
	set := ScopeSet{}
	for _, f := range fields {
		g := strings.ToLower(strings.TrimSpace(f))
		if g == "" {
			continue
		}
		if _, ok := knownGrants[g]; !ok {
			return "", fmt.Errorf("unknown grant %q (valid: %s)", g, strings.Join(AllGrants(), ", "))
		}
		set[g] = struct{}{}
	}
	if len(set) == 0 {
		return "", fmt.Errorf("no valid grants in %q", requested)
	}
	return set.Canonical(), nil
}

func setOf(grants []string) map[string]struct{} {
	m := make(map[string]struct{}, len(grants))
	for _, g := range grants {
		m[g] = struct{}{}
	}
	return m
}
