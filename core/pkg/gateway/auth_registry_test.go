package gateway

import (
	"database/sql"
	"strings"
	"testing"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// A namespace gateway's own RQLite is not the registry. The challenge was
// written to the registry and claimed from the tenant database, so every
// wallet sign-in through a namespace gateway failed with "no such table:
// nonces" (stagenet, 2026-09-29).
func TestBindAuthRegistry_namespaceGatewayClaimsTheNonceInTheRegistry(t *testing.T) {
	g, _, registry := keyRegistry(t)
	tenant, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open tenant db: %v", err)
	}
	t.Cleanup(func() { _ = tenant.Close() })

	bindAuthRegistry(g.authService, &Dependencies{
		ORMClient:       rqlite.NewClient(tenant),
		GlobalORMClient: rqlite.NewClient(registry),
	})

	const wallet = "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"
	c, err := g.authService.CreateChallenge(t.Context(), gwauth.ChallengeParams{
		Wallet: wallet, Namespace: "anchat", Chain: siw.Ethereum,
		Domain: "ns-anchat.orama.example", URI: "https://ns-anchat.orama.example",
	})
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if err := g.authService.ConsumeNonce(t.Context(), wallet, c.Nonce, "anchat"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Single use still holds against the registry.
	if err := g.authService.ConsumeNonce(t.Context(), wallet, c.Nonce, "anchat"); err == nil {
		t.Fatal("a claimed challenge was claimed again")
	}
}

// With no rqlite client at all the service refuses to claim rather than
// claiming without an affected-row count.
func TestBindAuthRegistry_noRegistryLeavesClaimsRefused(t *testing.T) {
	g, _, _ := keyRegistry(t)
	bindAuthRegistry(g.authService, &Dependencies{})

	err := g.authService.ConsumeNonce(t.Context(), "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB", "0123456789abcdef", "anchat")
	if err == nil || !strings.Contains(err.Error(), "SetRqliteClient") {
		t.Fatalf("err = %v, want the not-configured refusal", err)
	}
}
