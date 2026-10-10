package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// rqliteAPIPath is where a gateway mounts its database API.
	rqliteAPIPath = "/v1/rqlite"
	// gatewayDatabaseTimeout bounds one database request to a gateway.
	gatewayDatabaseTimeout = 60 * time.Second
	// maxDatabaseResponse caps what one database response is read into memory.
	maxDatabaseResponse = 64 << 20
)

// gatewayDatabaseClient is the DatabaseClient of a client configured without
// DatabaseEndpoints. It reaches the database through the gateway's
// /v1/rqlite API with the client's own credential, as every other client of
// the SDK reaches the gateway, so a program outside the WireGuard mesh, which
// cannot dial an RQLite node, can use it. A client with DatabaseEndpoints
// dials RQLite directly (DatabaseClientImpl): that is how a gateway reaches
// its own database.
type gatewayDatabaseClient struct {
	client *Client
	http   *http.Client
}

func newGatewayDatabaseClient(c *Client) *gatewayDatabaseClient {
	// No redirects: a redirect is answered as it is, never followed. Go strips
	// Authorization on a cross-host hop but forwards X-API-Key, which the
	// request carries too.
	return &gatewayDatabaseClient{client: c, http: &http.Client{
		Timeout:       gatewayDatabaseTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// checkAccess is what every call needs before it is sent.
func (d *gatewayDatabaseClient) checkAccess(ctx context.Context) error {
	if !d.client.isConnected() {
		return fmt.Errorf("client not connected")
	}
	if err := d.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}
	return nil
}

// Query runs a read or a write, chosen from the statement, through the gateway.
func (d *gatewayDatabaseClient) Query(ctx context.Context, sql string, args ...interface{}) (*QueryResult, error) {
	if err := d.checkAccess(ctx); err != nil {
		return nil, err
	}
	body := map[string]any{"sql": sql}
	if len(args) > 0 {
		body["args"] = args
	}

	if isWriteStatement(sql) {
		var out struct {
			RowsAffected int64 `json:"rows_affected"`
			LastInsertID int64 `json:"last_insert_id"`
		}
		if err := d.post(ctx, "/exec", body, &out); err != nil {
			return nil, fmt.Errorf("query failed: %w", err)
		}
		return &QueryResult{
			Columns:      []string{"affected"},
			Rows:         [][]interface{}{{"success"}},
			Count:        1,
			LastInsertID: out.LastInsertID,
			RowsAffected: out.RowsAffected,
		}, nil
	}

	var out struct {
		Items   []map[string]json.RawMessage `json:"items"`
		Columns []string                     `json:"columns"`
	}
	if err := d.post(ctx, "/query", body, &out); err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	if err := checkResultColumns(len(out.Items), out.Columns); err != nil {
		return nil, err
	}
	result := &QueryResult{
		Columns: out.Columns,
		Rows:    make([][]interface{}, 0, len(out.Items)),
		Count:   int64(len(out.Items)),
	}
	for _, item := range out.Items {
		row := make([]interface{}, len(out.Columns))
		for i, column := range out.Columns {
			value, err := decodeCell(item[column])
			if err != nil {
				return nil, fmt.Errorf("column %s of the result is not readable: %w", column, err)
			}
			row[i] = value
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

// checkResultColumns refuses a result whose rows cannot be put in the
// statement's column order. The gateway sends each row as a map keyed by
// column name plus the names in order; with no names (a gateway from before
// it sent them) every row would decode empty, and with a name twice (SELECT
// a.id, b.id) one of the two values is already lost in the map.
func checkResultColumns(rows int, columns []string) error {
	if rows > 0 && len(columns) == 0 {
		return fmt.Errorf("the gateway did not say the result's column order; upgrade the gateway")
	}
	seen := make(map[string]bool, len(columns))
	for _, c := range columns {
		if seen[c] {
			return fmt.Errorf("the result has two columns named %q; give them distinct names with AS", c)
		}
		seen[c] = true
	}
	return nil
}

// Transaction runs statements atomically: all of them, or none.
func (d *gatewayDatabaseClient) Transaction(ctx context.Context, queries []string) error {
	if err := d.checkAccess(ctx); err != nil {
		return err
	}
	if len(queries) == 0 {
		return nil
	}
	var out struct {
		Status      string `json:"status"`
		FailedIndex int    `json:"failed_index"`
		Error       string `json:"error"`
	}
	err := d.post(ctx, "/transaction", map[string]any{"statements": queries}, &out)
	var refused *gatewayStatusError
	if errors.As(err, &refused) && refused.status == http.StatusConflict && out.Status == "rollback" {
		return fmt.Errorf("transaction rolled back at statement %d: %s", out.FailedIndex, out.Error)
	}
	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}
	return nil
}

// CreateTable runs a CREATE TABLE statement.
func (d *gatewayDatabaseClient) CreateTable(ctx context.Context, schema string) error {
	if err := d.checkAccess(ctx); err != nil {
		return err
	}
	if err := d.post(ctx, "/create-table", map[string]any{"schema": schema}, nil); err != nil {
		return fmt.Errorf("create table failed: %w", err)
	}
	return nil
}

// DropTable drops a table; dropping one that is not there is not an error.
func (d *gatewayDatabaseClient) DropTable(ctx context.Context, tableName string) error {
	if err := d.checkAccess(ctx); err != nil {
		return err
	}
	err := d.post(ctx, "/drop-table", map[string]any{"table": tableName}, nil)
	var refused *gatewayStatusError
	if errors.As(err, &refused) && refused.status == http.StatusNotFound && strings.Contains(refused.message, "no such table") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("drop table failed: %w", err)
	}
	return nil
}

// GetSchema lists the tables and their columns.
//
// A table the gateway will not let this credential query (the platform's own
// tables share a namespace's database) is not part of its schema and is left
// out; any other failure is returned.
func (d *gatewayDatabaseClient) GetSchema(ctx context.Context) (*SchemaInfo, error) {
	if err := d.checkAccess(ctx); err != nil {
		return nil, err
	}
	var listed struct {
		Tables []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"tables"`
	}
	if err := d.get(ctx, "/schema", &listed); err != nil {
		return nil, fmt.Errorf("failed to query table list: %w", err)
	}

	schema := &SchemaInfo{Tables: make([]TableInfo, 0, len(listed.Tables))}
	for _, table := range listed.Tables {
		if table.Type != "table" {
			continue
		}
		var columns struct {
			Items []struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				NotNull int64  `json:"notnull"`
			} `json:"items"`
		}
		pragma := fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(table.Name))
		err := d.post(ctx, "/query", map[string]any{"sql": pragma}, &columns)
		var refused *gatewayStatusError
		if errors.As(err, &refused) && refused.status == http.StatusForbidden {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read the columns of %s: %w", table.Name, err)
		}
		info := TableInfo{Name: table.Name, Columns: make([]ColumnInfo, 0, len(columns.Items))}
		for _, c := range columns.Items {
			info.Columns = append(info.Columns, ColumnInfo{Name: c.Name, Type: c.Type, Nullable: c.NotNull == 0})
		}
		schema.Tables = append(schema.Tables, info)
	}
	return schema, nil
}

func (d *gatewayDatabaseClient) post(ctx context.Context, path string, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to encode the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, getGatewayURL(d.client)+rqliteAPIPath+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return d.do(req, out)
}

func (d *gatewayDatabaseClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getGatewayURL(d.client)+rqliteAPIPath+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	return d.do(req, out)
}

// do sends req with the client's credential. An error status comes back as a
// *gatewayStatusError, and out is still filled from a JSON body when there is
// one, since a refusal such as a rollback says more than its status.
func (d *gatewayDatabaseClient) do(req *http.Request, out any) error {
	addAuthHeaders(req, d.client)
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return fmt.Errorf("the gateway redirected the request (%d to %q); the database client does not follow redirects, so point it at the gateway itself",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDatabaseResponse+1))
	if err != nil {
		return fmt.Errorf("failed to read the response: %w", err)
	}
	if len(raw) > maxDatabaseResponse {
		return fmt.Errorf("the response is over %d bytes; select fewer rows", maxDatabaseResponse)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil && resp.StatusCode < http.StatusBadRequest {
			return fmt.Errorf("failed to decode the response: %w", err)
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return &gatewayStatusError{status: resp.StatusCode, message: gatewayErrorMessage(raw)}
	}
	return nil
}

// decodeCell decodes one result value. An integer is an int64 and any other
// number a float64, as a SQLite column is one or the other.
func decodeCell(raw json.RawMessage) (interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if number, ok := value.(json.Number); ok {
		if i, err := number.Int64(); err == nil {
			return i, nil
		}
		return number.Float64()
	}
	return value, nil
}

// quoteIdentifier quotes a table name for SQLite.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
