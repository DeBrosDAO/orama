package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// resolveNamespaceID ensures the given namespace exists and returns its primary key ID.
// Falls back to "default" when ns is empty. Uses internal auth context for system operations.
func (g *Gateway) resolveNamespaceID(ctx context.Context, ns string) (interface{}, error) {
	if g == nil || g.client == nil {
		return nil, errors.New("client not initialized")
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		ns = "default"
	}

	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()

	if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", ns); err != nil {
		return nil, err
	}
	res, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, errors.New("failed to resolve namespace")
	}
	return res.Rows[0][0], nil
}
