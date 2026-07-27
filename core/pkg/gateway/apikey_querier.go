package gateway

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// apiKeyQuerier is the minimal query capability API-key lookups need. It
// exists so lookupAPIKeyEntry can be pointed at THIS gateway's own
// namespace-bound RQLite (g.sqlDB, opened against cfg.RQLiteDSN by
// initializeRQLite in dependencies.go) instead of g.client, which is
// accidentally core-bound: client.DefaultClientConfig() pre-populates
// DatabaseEndpoints from bootstrap peers on port 5001, so the namespace DSN
// override in NewDependencies never applies. `orama namespace keys create`
// writes keys into the namespace RQLite, so validation must read from there
// too — otherwise every namespace key 401s.
type apiKeyQuerier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*client.QueryResult, error)
}

// sqlAPIKeyQuerier implements apiKeyQuerier directly over *sql.DB (g.sqlDB).
type sqlAPIKeyQuerier struct {
	db *sql.DB
}

// Query runs sql against the underlying *sql.DB and scans every row into
// [][]interface{}, matching client.QueryResult's shape. The rqlite stdlib
// driver (github.com/rqlite/gorqlite/stdlib) hands back already-decoded JSON
// values (string/float64/bool/nil) as driver.Value, so scanning into
// *interface{} yields the same Go types client.DatabaseClient.Query already
// returns via gorqlite's Slice() — callers like getString/getInt work
// unchanged against either source.
func (q *sqlAPIKeyQuerier) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	if q == nil || q.db == nil {
		return nil, fmt.Errorf("sqlAPIKeyQuerier: db is nil, cannot run query %q", query)
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlAPIKeyQuerier: query failed: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlAPIKeyQuerier: reading columns failed: %w", err)
	}

	resultRows := make([][]interface{}, 0)
	for rows.Next() {
		values := make([]interface{}, len(columns))
		scanTargets := make([]interface{}, len(columns))
		for i := range values {
			scanTargets[i] = &values[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return nil, fmt.Errorf("sqlAPIKeyQuerier: scanning row failed: %w", err)
		}
		resultRows = append(resultRows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlAPIKeyQuerier: iterating rows failed: %w", err)
	}

	return &client.QueryResult{
		Columns: columns,
		Rows:    resultRows,
		Count:   int64(len(resultRows)),
	}, nil
}

// apiKeyDB returns the querier API-key lookups should use: this gateway's own
// namespace-bound RQLite (g.sqlDB) when available, falling back to g.client's
// database handle only if sqlDB was never initialized (e.g. RQLite init
// failed at startup — see initializeRQLite). Returns nil when neither is
// available; callers must surface an error rather than silently treating
// every key as invalid.
func (g *Gateway) apiKeyDB() apiKeyQuerier {
	if g.sqlDB != nil {
		return &sqlAPIKeyQuerier{db: g.sqlDB}
	}
	if g.client != nil {
		return g.client.Database()
	}
	return nil
}
