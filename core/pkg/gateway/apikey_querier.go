package gateway

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// apiKeyQuerier is the minimal query capability API-key lookups need.
//
// API keys are authoritatively stored in the GLOBAL/CORE RQLite registry,
// HMAC-hashed (docs/SECURITY.md:54-59). `orama namespace keys create` and
// POST /v1/namespace/keys write there, and ONLY there. A namespace gateway's
// own RQLite (g.sqlDB) may hold a stale, unrelated api_keys table -- it is
// never authoritative and must never be queried for key validation. Every
// gateway, main or namespace, must resolve keys against the same global
// registry so the external (main gateway) and internal (namespace gateway,
// e.g. WASM http_fetch) validation paths agree.
type apiKeyQuerier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*client.QueryResult, error)
}

// apiKeyDB returns the querier API-key lookups should use: the explicit
// global-registry client (g.authClient, built from cfg.GlobalRQLiteDSN -- see
// New in gateway.go) when available, falling back to g.client only when no
// dedicated global client was configured (the index gateway, where
// rqlite_dsn IS the registry).
//
// The fallback is reachable only on a gateway that was never configured with a
// registry of its own. A gateway that was configured with one and could not
// reach it does not finish booting, so this cannot quietly become "validate
// keys against whatever database is at hand": g.client on a namespace gateway
// is the tenant's own rqlite (bugboard #162), whose api_keys table the tenant
// can write.
//
// g.sqlDB (this gateway's own namespace RQLite) must NEVER be used here --
// its api_keys table is not authoritative, and querying it split key
// validation into two disagreeing registries (bugboard #151/#152
// regression). Returns nil when neither is available; callers must surface
// an error rather than silently treating every key as invalid.
func (g *Gateway) apiKeyDB() apiKeyQuerier {
	if g.authClient != nil {
		return g.authClient.Database()
	}
	if g.client != nil {
		return g.client.Database()
	}
	return nil
}
