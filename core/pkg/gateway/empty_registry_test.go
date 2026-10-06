package gateway

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// emptyRegistryNet is a registry with nothing in it. A Service with no database
// refuses every credential (the revocation list cannot be read), so a test that
// verifies tokens gives it one that answers.
type emptyRegistryNet struct{ client.NetworkClient }

func (emptyRegistryNet) Database() client.DatabaseClient { return emptyRegistryDB{} }

type emptyRegistryDB struct{ client.DatabaseClient }

func (emptyRegistryDB) Query(context.Context, string, ...interface{}) (*client.QueryResult, error) {
	return &client.QueryResult{}, nil
}
