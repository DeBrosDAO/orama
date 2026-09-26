package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// A namespace's own rqlite has an api_keys table, because the core migrations
// run there. Nothing validates against it — keys are created in the cluster's
// registry and read from there — but rows written before that was true are
// still on disk, and the ones from before bugboard #163 hold the raw `ak_…`
// value rather than its hash.
//
// So a tenant's database contains working credentials for the platform, in the
// clear, that the tenant can read. MigratePlaintextAPIKeys hashes such rows,
// but it runs against the registry and never sees these.
//
// They are not migrated here, they are removed. Hashing a row nothing reads
// would keep a useless record of a credential; the row's only remaining
// property is that it is a secret sitting where it should not be.

// purgeTenantPlaintextAPIKeys deletes plaintext `ak_` rows from a database that
// is not the key registry. Hashed rows are left alone: they are inert too, but
// they are not credentials, and deleting more than the thing that is wrong is
// how a cleanup becomes a data loss.
//
// Returns the number of rows removed.
func purgeTenantPlaintextAPIKeys(ctx context.Context, db apiKeyQuerier) (int, error) {
	if db == nil {
		return 0, nil
	}

	internalCtx := client.WithInternalAuth(ctx)
	res, err := db.Query(internalCtx, "SELECT COUNT(*) FROM api_keys WHERE key LIKE 'ak_%' OR key LIKE 'orama_%'")
	if err != nil {
		// A namespace database with no api_keys table holds no keys, which is
		// the state this function wants. Any other failure — no leader, a
		// timeout, a refused connection — says nothing about whether the
		// credentials are still there, so it fails the step and readiness
		// retries it.
		if isNoSuchTableError(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("count plaintext api keys in this namespace's database: %w", err)
	}
	if countFromResult(res) == 0 {
		return 0, nil
	}

	before := countFromResult(res)
	if _, err := db.Query(internalCtx, "DELETE FROM api_keys WHERE key LIKE 'ak_%' OR key LIKE 'orama_%'"); err != nil {
		return 0, fmt.Errorf("remove plaintext api keys from this namespace's database: %w", err)
	}
	return before, nil
}

// countFromResult reads a single COUNT(*) cell.
func countFromResult(res *client.QueryResult) int {
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0
	}
	switch v := res.Rows[0][0].(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// isNoSuchTableError reports whether err is SQLite's "no such table", which is
// what rqlite relays for a query against a table that does not exist.
func isNoSuchTableError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such table")
}
