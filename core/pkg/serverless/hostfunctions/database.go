package hostfunctions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// DBQuery executes a SELECT query and returns JSON-encoded results.
func (h *HostFunctions) DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: serverless.ErrDatabaseUnavailable}
	}

	var results []map[string]interface{}
	if err := h.db.Query(ctx, &results, query, args...); err != nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: err}
	}

	data, err := json.Marshal(results)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: fmt.Errorf("failed to marshal results: %w", err)}
	}

	return data, nil
}

// DBExecute executes an INSERT/UPDATE/DELETE query and returns affected rows.
func (h *HostFunctions) DBExecute(ctx context.Context, query string, args []interface{}) (int64, error) {
	if h.db == nil {
		return 0, &serverless.HostFunctionError{Function: "db_execute", Cause: serverless.ErrDatabaseUnavailable}
	}

	result, err := h.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, &serverless.HostFunctionError{Function: "db_execute", Cause: err}
	}

	affected, _ := result.RowsAffected()
	return affected, nil
}
