package gateway

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func (g *Gateway) resolveNamespaceID(ctx context.Context, ns string) (interface{}, error) {
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", ns); err != nil {
		return nil, err
	}
	res, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns)
	if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, err
	}
	return res.Rows[0][0], nil
}

// Deprecated: seeding API keys from config is removed.
