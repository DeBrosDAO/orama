package turn

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Bugboard #283. TURN binds the well-known ports 3478/5349, which are exclusive
// per host, so one TURN process per namespace meant only ONE namespace could have
// TURN on a given node — the second crash-looped on bind. Moving each namespace to
// its own ports would work but put TURN on arbitrary high ports, which restrictive
// networks routinely block: the worst outcome for the users who most need a relay.
//
// So one server now serves many namespaces. Credentials already carry the
// namespace ("{expiry}:{namespace}"), so each tenant authenticates against its OWN
// secret with no protocol change and nothing new for clients to dial.
//
// That makes TURN a cross-tenant component, and the per-tenant secret lookup is
// the isolation boundary. These tests exist to hold that boundary: a shared relay
// that authorizes one tenant with another's secret would be the same class of bug
// as the cross-namespace raft join (#275), in the media path.

func multiTenantConfig() Config {
	return Config{
		ListenAddr:     "0.0.0.0:3478",
		PublicIP:       "203.0.113.1",
		Realm:          "orama-devnet.network",
		RelayPortStart: 49152,
		RelayPortEnd:   50000,
		Tenants: []TenantConfig{
			{Namespace: "anchat-test", AuthSecret: "secret-for-anchat-test"},
			{Namespace: "anchat-v2", AuthSecret: "secret-for-anchat-v2"},
		},
	}
}

// Each tenant resolves to its own secret — never a neighbour's.
func TestTenantSecret_isolatesTenants(t *testing.T) {
	cfg := multiTenantConfig()

	for _, tc := range []struct{ ns, want string }{
		{"anchat-test", "secret-for-anchat-test"},
		{"anchat-v2", "secret-for-anchat-v2"},
	} {
		got, ok := cfg.TenantSecret(tc.ns)
		if !ok {
			t.Fatalf("TenantSecret(%q) reported not served", tc.ns)
		}
		if got != tc.want {
			t.Errorf("TenantSecret(%q) = %q, want %q — a tenant must never resolve to another's secret", tc.ns, got, tc.want)
		}
	}
}

// A namespace this server does not serve must be rejected outright — never
// silently authorized against some default.
func TestTenantSecret_unknownNamespaceIsRejected(t *testing.T) {
	cfg := multiTenantConfig()

	if secret, ok := cfg.TenantSecret("some-other-tenant"); ok {
		t.Errorf("TenantSecret returned ok for an unserved namespace (secret=%q) — this is the cross-tenant relay hole", secret)
	}
}

// The credential-validation path must agree: a credential minted with tenant A's
// secret must not validate for tenant B.
func TestValidateCredentials_secretIsNotInterchangeableAcrossTenants(t *testing.T) {
	const (
		secretA = "secret-for-anchat-test"
		secretB = "secret-for-anchat-v2"
	)
	// A well-formed, unexpired credential for anchat-v2.
	username := fmt.Sprintf("%d:%s", farFutureUnix(), "anchat-v2")
	passwordB := GeneratePassword(secretB, username)

	if !ValidateCredentials(secretB, username, passwordB, "anchat-v2") {
		t.Fatal("a tenant's own credential failed to validate")
	}
	if ValidateCredentials(secretA, username, passwordB, "anchat-v2") {
		t.Error("a credential validated under a DIFFERENT tenant's secret — tenants would share a relay identity")
	}
}

// The namespace in the username is load-bearing: a credential for one namespace
// must not validate as another even with the right secret.
func TestValidateCredentials_namespaceIsBoundIntoTheCredential(t *testing.T) {
	const secret = "shared-looking-secret"
	username := fmt.Sprintf("%d:%s", farFutureUnix(), "anchat-v2")
	password := GeneratePassword(secret, username)

	if ValidateCredentials(secret, username, password, "anchat-test") {
		t.Error("an anchat-v2 credential validated as anchat-test")
	}
}

// The legacy single-tenant form must keep working across the rollout: an existing
// config carries Namespace + AuthSecret and no tenants list.
func TestResolvedTenants_normalizesLegacySingleTenantConfig(t *testing.T) {
	cfg := Config{Namespace: "anchat-test", AuthSecret: "legacy-secret"}

	tenants := cfg.ResolvedTenants()
	if len(tenants) != 1 {
		t.Fatalf("got %d tenants, want 1 — an existing config must keep working", len(tenants))
	}
	if tenants[0].Namespace != "anchat-test" || tenants[0].AuthSecret != "legacy-secret" {
		t.Errorf("legacy config normalized incorrectly: %+v", tenants[0])
	}
	if secret, ok := cfg.TenantSecret("anchat-test"); !ok || secret != "legacy-secret" {
		t.Errorf("legacy tenant lookup failed: secret=%q ok=%v", secret, ok)
	}
	if _, ok := cfg.TenantSecret("anchat-v2"); ok {
		t.Error("a legacy single-tenant config must not authorize any other namespace")
	}
}

// A tenant entry with an empty secret must not authorize anything — otherwise a
// half-written config would authorize a namespace against an empty HMAC key.
func TestTenantSecret_emptySecretIsNotAuthorized(t *testing.T) {
	cfg := Config{Tenants: []TenantConfig{{Namespace: "broken", AuthSecret: ""}}}

	if _, ok := cfg.TenantSecret("broken"); ok {
		t.Error("a tenant with an empty secret was authorized")
	}
}

// Validation must reject a duplicate namespace rather than silently picking one
// of the two secrets by list order.
func TestValidate_rejectsDuplicateTenantNamespace(t *testing.T) {
	cfg := multiTenantConfig()
	cfg.Tenants = append(cfg.Tenants, TenantConfig{Namespace: "anchat-v2", AuthSecret: "other-secret"})

	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if e != nil && strings.Contains(e.Error(), "listed more than once") {
			found = true
		}
	}
	if !found {
		t.Errorf("duplicate tenant namespace was accepted; errors = %v", errs)
	}
}

// A tenant missing its secret must fail validation, not start a server that
// authorizes it with nothing.
func TestValidate_rejectsTenantWithoutSecret(t *testing.T) {
	cfg := multiTenantConfig()
	cfg.Tenants[1].AuthSecret = ""

	errs := cfg.Validate()
	if len(errs) == 0 {
		t.Error("a tenant with no auth secret passed validation")
	}
}

// A fully-populated multi-tenant config is valid.
func TestValidate_acceptsMultiTenantConfig(t *testing.T) {
	cfg := multiTenantConfig()
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("valid multi-tenant config rejected: %v", errs)
	}
}

// farFutureUnix is an expiry comfortably beyond any test run.
func farFutureUnix() int64 { return time.Now().Add(time.Hour).Unix() }
