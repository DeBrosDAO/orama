package auth

import (
	"strings"
	"testing"
)

// Every Scope* constant must be mintable. ScopePubsub was declared, demanded by
// requiredScope for every /v1/pubsub/ path, listed in DataPlaneScopes and named
// in NormalizeGrants' own error message — and missing from knownGrants, so
// `--scope pubsub` was rejected as unknown and no API key could ever hold it.
func TestEveryDeclaredScopeCanBeGranted(t *testing.T) {
	declared := []string{
		ScopeAdmin,
		ScopeInvoke,
		ScopeStorage,
		ScopePush,
		ScopeWebRTC,
		ScopeProxy,
		ScopePubsub,
		ScopeCache,
	}

	for _, grant := range declared {
		stored, err := NormalizeGrants(grant)
		if err != nil {
			t.Errorf("NormalizeGrants(%q) rejected a declared scope: %v", grant, err)
			continue
		}
		if !ParseScopes(stored).Has(grant) {
			t.Errorf("NormalizeGrants(%q) stored %q, which does not hold that grant", grant, stored)
		}
	}
}

func TestPubsubGrantIsMintableAndSatisfiesPubsubRoutes(t *testing.T) {
	stored, err := NormalizeGrants("pubsub")
	if err != nil {
		t.Fatalf("pubsub must be a mintable grant: %v", err)
	}
	if stored != ScopePubsub {
		t.Fatalf("stored = %q, want %q", stored, ScopePubsub)
	}

	set := ScopesFromStored(stored)
	if !set.Has(ScopePubsub) {
		t.Error("a pubsub-scoped key does not satisfy the pubsub grant")
	}
	if set.Has(ScopeAdmin) {
		t.Error("a pubsub-scoped key must not satisfy admin")
	}
	if set.Has(ScopeStorage) {
		t.Error("a pubsub-scoped key must not satisfy storage")
	}
}

func TestAllGrantsMatchesKnownGrants(t *testing.T) {
	all := AllGrants()
	if len(all) != len(knownGrants) {
		t.Fatalf("AllGrants() has %d entries, knownGrants has %d", len(all), len(knownGrants))
	}
	for _, g := range all {
		if _, ok := knownGrants[g]; !ok {
			t.Errorf("AllGrants() returned %q, which is not a known grant", g)
		}
	}
	// Sorted, so the list a client is shown is stable.
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Fatalf("AllGrants() is not sorted: %v", all)
		}
	}
}

// The rejection message must name the grants that actually exist. It used to be
// a hand-written string, which is how it came to advertise a grant the
// validator refused.
func TestUnknownGrantErrorListsTheRealGrants(t *testing.T) {
	_, err := NormalizeGrants("nonsense")
	if err == nil {
		t.Fatal("an unknown grant must be rejected")
	}
	for _, g := range AllGrants() {
		if !strings.Contains(err.Error(), g) {
			t.Errorf("error message does not mention the valid grant %q: %s", g, err)
		}
	}
	if strings.Contains(err.Error(), "nonsense,") {
		t.Errorf("the rejected grant leaked into the valid list: %s", err)
	}
}

// Every grant a logged-in user receives must also be one a key can be minted
// with, or the two authorization paths disagree about what exists.
func TestDataPlaneScopesAreAllMintable(t *testing.T) {
	for grant := range DataPlaneScopes() {
		if _, err := NormalizeGrants(grant); err != nil {
			t.Errorf("DataPlaneScopes grants %q but it cannot be minted: %v", grant, err)
		}
	}
}
