package rqlite

// HTTP gateway for the rqlite ORM client.
//
// This file exposes a minimal, SDK-friendly HTTP interface over the ORM-like
// client defined in client.go. It maps high-level operations (Query, Exec,
// FindBy, FindOneBy, QueryBuilder-based SELECTs, Transactions) and a few schema
// helpers into JSON-over-HTTP endpoints that can be called from any language.
//
// Endpoints (under BasePath, default: /v1/db):
//   - POST  {base}/query           -> arbitrary SELECT; returns rows as []map[string]any
//   - POST  {base}/exec            -> write statement (INSERT/UPDATE/DELETE/DDL); returns {rows_affected,last_insert_id}
//   - POST  {base}/find            -> FindBy(table, criteria, opts...) -> returns []map
//   - POST  {base}/find-one        -> FindOneBy(table, criteria, opts...) -> returns map
//   - POST  {base}/select          -> Fluent SELECT builder via JSON (joins, where, order, group, limit, offset); returns []map or one map if one=true
//   - POST  {base}/transaction     -> Execute a sequence of exec/query ops atomically; optionally return results
//
// Schema helpers (convenience; powered via Exec/Query):
//   - GET   {base}/schema          -> list of user tables/views and create SQL
//   - POST  {base}/create-table    -> {schema: "CREATE TABLE ..."} -> status ok
//   - POST  {base}/drop-table      -> {table: "name"} -> status ok (safe-validated identifier)
//
// Notes:
// - All numbers in JSON are decoded as float64 by default; we best-effort coerce
//   integral values to int64 for SQL placeholders.
// - The Save/Remove reflection helpers in the ORM require concrete Go structs;
//   exposing them generically over HTTP is not portable. Prefer using the Exec
//   and Find APIs, or the Select builder for CRUD-like flows.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// HTTPGateway exposes the ORM Client as a set of HTTP handlers.
type HTTPGateway struct {
	// Client is the ORM-like rqlite client to execute operations against.
	Client Client
	// BasePath is the prefix for all routes, e.g. "/v1/db".
	// If empty, defaults to "/v1/db". A trailing slash is trimmed.
	BasePath string

	// Optional: Request timeout. If > 0, handlers will use a context with this timeout.
	Timeout time.Duration
}

// NewHTTPGateway constructs a new HTTPGateway with sensible defaults.
func NewHTTPGateway(c Client, base string) *HTTPGateway {
	return &HTTPGateway{
		Client:   c,
		BasePath: base,
	}
}

// RegisterRoutes registers all handlers onto the provided mux under BasePath.
func (g *HTTPGateway) RegisterRoutes(mux *http.ServeMux) {
	base := g.base()
	mux.HandleFunc(base+"/query", g.handleQuery)
	mux.HandleFunc(base+"/exec", g.handleExec)
	mux.HandleFunc(base+"/find", g.handleFind)
	mux.HandleFunc(base+"/find-one", g.handleFindOne)
	mux.HandleFunc(base+"/select", g.handleSelect)
	// Keep "transaction" for compatibility with existing routes.
	mux.HandleFunc(base+"/transaction", g.handleTransaction)

	// Schema helpers
	mux.HandleFunc(base+"/schema", g.handleSchema)
	mux.HandleFunc(base+"/create-table", g.handleCreateTable)
	mux.HandleFunc(base+"/drop-table", g.handleDropTable)
}

func (g *HTTPGateway) base() string {
	b := strings.TrimSpace(g.BasePath)
	if b == "" {
		b = "/v1/db"
	}
	if b != "/" {
		b = strings.TrimRight(b, "/")
	}
	return b
}

func (g *HTTPGateway) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if g.Timeout > 0 {
		return context.WithTimeout(ctx, g.Timeout)
	}
	return context.WithCancel(ctx)
}

// --------------------
// Common HTTP helpers
// --------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func onlyMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

// Normalize JSON-decoded args for SQL placeholders.
// - Convert float64 with integral value to int64 to better match SQLite expectations.
// - Leave strings, bools and nulls as-is.
// - Recursively normalizes nested arrays if present.
func normalizeArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case float64:
			// If v is integral (within epsilon), convert to int64
			if v == float64(int64(v)) {
				out[i] = int64(v)
			} else {
				out[i] = v
			}
		case []any:
			out[i] = normalizeArgs(v)
		default:
			out[i] = a
		}
	}
	return out
}

// --------------------
// Request DTOs
// --------------------

type queryRequest struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args"`
}

type execRequest struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args"`
}

type findOptions struct {
	Select  []string   `json:"select"`
	OrderBy []string   `json:"order_by"`
	GroupBy []string   `json:"group_by"`
	Limit   *int       `json:"limit"`
	Offset  *int       `json:"offset"`
	Joins   []joinBody `json:"joins"`
}

type findRequest struct {
	Table    string         `json:"table"`
	Criteria map[string]any `json:"criteria"`
	Options  findOptions    `json:"options"`
	// Back-compat: allow options at top-level too
	Select  []string   `json:"select"`
	OrderBy []string   `json:"order_by"`
	GroupBy []string   `json:"group_by"`
	Limit   *int       `json:"limit"`
	Offset  *int       `json:"offset"`
	Joins   []joinBody `json:"joins"`
}

type findOneRequest = findRequest

type joinBody struct {
	Kind  string `json:"kind"`  // "INNER" | "LEFT" | "JOIN"
	Table string `json:"table"` // table name
	On    string `json:"on"`    // join condition
}

type whereBody struct {
	Conj string `json:"conj"` // "AND" | "OR" (default AND)
	Expr string `json:"expr"` // e.g., "a = ? AND b > ?"
	Args []any  `json:"args"`
}

type selectRequest struct {
	Table   string      `json:"table"`
	Alias   string      `json:"alias"`
	Select  []string    `json:"select"`
	Joins   []joinBody  `json:"joins"`
	Where   []whereBody `json:"where"`
	GroupBy []string    `json:"group_by"`
	OrderBy []string    `json:"order_by"`
	Limit   *int        `json:"limit"`
	Offset  *int        `json:"offset"`
	One     bool        `json:"one"` // if true, returns a single row (object)
}

type txOp struct {
	Kind string `json:"kind"` // "exec" | "query"
	SQL  string `json:"sql"`
	Args []any  `json:"args"`
}

type transactionRequest struct {
	Ops            []txOp   `json:"ops"`
	Statements     []string `json:"statements"`      // legacy format: array of SQL strings (treated as exec ops)
	ReturnResults  bool     `json:"return_results"`  // if true, returns per-op results
	StopOnError    bool     `json:"stop_on_error"`   // default true in tx
	PartialResults bool     `json:"partial_results"` // ignored for actual TX (atomic); kept for API symmetry
}

// --------------------
// Handlers
// --------------------

func (g *HTTPGateway) handleQuery(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body queryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SQL) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {sql, args?}")
		return
	}
	args := normalizeArgs(body.Args)
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	out := make([]map[string]any, 0, 16)
	if err := g.Client.Query(ctx, &out, body.SQL, args...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"count": len(out),
	})
}

func (g *HTTPGateway) handleExec(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body execRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SQL) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {sql, args?}")
		return
	}
	args := normalizeArgs(body.Args)
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	res, err := g.Client.Exec(ctx, body.SQL, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	liid, _ := res.LastInsertId()
	ra, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{
		"rows_affected":   ra,
		"last_insert_id":  liid,
		"execution_state": "ok",
	})
}

func (g *HTTPGateway) handleFind(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body findRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {table, criteria, options?}")
		return
	}
	opts := makeFindOptions(mergeFindOptions(body))
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	out := make([]map[string]any, 0, 32)
	if err := g.Client.FindBy(ctx, &out, body.Table, body.Criteria, opts...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"count": len(out),
	})
}

func (g *HTTPGateway) handleFindOne(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body findOneRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {table, criteria, options?}")
		return
	}
	opts := makeFindOptions(mergeFindOptions(body))
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	row := make(map[string]any)
	if err := g.Client.FindOneBy(ctx, &row, body.Table, body.Criteria, opts...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (g *HTTPGateway) handleSelect(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body selectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {table, select?, where?, joins?, order_by?, group_by?, limit?, offset?, one?}")
		return
	}
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	qb := g.Client.CreateQueryBuilder(body.Table)
	if alias := strings.TrimSpace(body.Alias); alias != "" {
		qb = qb.Alias(alias)
	}
	if len(body.Select) > 0 {
		qb = qb.Select(body.Select...)
	}
	// joins
	for _, j := range body.Joins {
		switch strings.ToUpper(strings.TrimSpace(j.Kind)) {
		case "INNER":
			qb = qb.InnerJoin(j.Table, j.On)
		case "LEFT":
			qb = qb.LeftJoin(j.Table, j.On)
		default:
			qb = qb.Join(j.Table, j.On)
		}
	}
	// where
	for _, wcl := range body.Where {
		switch strings.ToUpper(strings.TrimSpace(wcl.Conj)) {
		case "OR":
			qb = qb.OrWhere(wcl.Expr, normalizeArgs(wcl.Args)...)
		default:
			qb = qb.AndWhere(wcl.Expr, normalizeArgs(wcl.Args)...)
		}
	}
	// group/order/limit/offset
	if len(body.GroupBy) > 0 {
		qb = qb.GroupBy(body.GroupBy...)
	}
	if len(body.OrderBy) > 0 {
		qb = qb.OrderBy(body.OrderBy...)
	}
	if body.Limit != nil {
		qb = qb.Limit(*body.Limit)
	}
	if body.Offset != nil {
		qb = qb.Offset(*body.Offset)
	}

	if body.One {
		row := make(map[string]any)
		if err := qb.GetOne(ctx, &row); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, row)
		return
	}

	rows := make([]map[string]any, 0, 32)
	if err := qb.GetMany(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rows,
		"count": len(rows),
	})
}

func (g *HTTPGateway) handleTransaction(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body transactionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: {ops:[{kind,sql,args?}], return_results?} or {statements:[sql...]}")
		return
	}

	// Support legacy "statements" format by converting to ops
	if len(body.Statements) > 0 && len(body.Ops) == 0 {
		body.Ops = make([]txOp, len(body.Statements))
		for i, stmt := range body.Statements {
			body.Ops[i] = txOp{Kind: "exec", SQL: stmt}
		}
	}

	if len(body.Ops) == 0 {
		writeError(w, http.StatusBadRequest, "invalid body: {ops:[{kind,sql,args?}], return_results?} or {statements:[sql...]}")
		return
	}
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	results := make([]any, 0, len(body.Ops))
	err := g.Client.Tx(ctx, func(tx Tx) error {
		for _, op := range body.Ops {
			switch strings.ToLower(strings.TrimSpace(op.Kind)) {
			case "exec":
				res, err := tx.Exec(ctx, op.SQL, normalizeArgs(op.Args)...)
				if err != nil {
					return err
				}
				if body.ReturnResults {
					li, _ := res.LastInsertId()
					ra, _ := res.RowsAffected()
					results = append(results, map[string]any{
						"rows_affected":  ra,
						"last_insert_id": li,
					})
				}
			case "query":
				var rows []map[string]any
				if err := tx.Query(ctx, &rows, op.SQL, normalizeArgs(op.Args)...); err != nil {
					return err
				}
				if body.ReturnResults {
					results = append(results, rows)
				}
			default:
				return fmt.Errorf("invalid op kind: %s", op.Kind)
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.ReturnResults {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"results": results,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// --------------------
// Schema helpers
// --------------------

func (g *HTTPGateway) handleSchema(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodGet) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	sqlText := `SELECT name, type, sql FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`
	var rows []map[string]any
	if err := g.Client.Query(ctx, &rows, sqlText); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tables": rows,
		"count":  len(rows),
	})
}

func (g *HTTPGateway) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body struct {
		Schema string `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Schema) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {schema}")
		return
	}
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	if _, err := g.Client.Exec(ctx, body.Schema); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok"})
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (g *HTTPGateway) handleDropTable(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	if g.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	var body struct {
		Table string `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {table}")
		return
	}
	tbl := strings.TrimSpace(body.Table)
	if !identRe.MatchString(tbl) {
		writeError(w, http.StatusBadRequest, "invalid table identifier")
		return
	}
	ctx, cancel := g.withTimeout(r.Context())
	defer cancel()

	stmt := "DROP TABLE " + tbl
	if _, err := g.Client.Exec(ctx, stmt); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// --------------------
// Helpers
// --------------------

func mergeFindOptions(fr findRequest) findOptions {
	// Prefer nested Options; fallback to top-level legacy fields
	if (len(fr.Options.Select)+len(fr.Options.OrderBy)+len(fr.Options.GroupBy)) > 0 ||
		fr.Options.Limit != nil || fr.Options.Offset != nil || len(fr.Options.Joins) > 0 {
		return fr.Options
	}
	return findOptions{
		Select:  fr.Select,
		OrderBy: fr.OrderBy,
		GroupBy: fr.GroupBy,
		Limit:   fr.Limit,
		Offset:  fr.Offset,
		Joins:   fr.Joins,
	}
}

func makeFindOptions(o findOptions) []FindOption {
	opts := make([]FindOption, 0, 6)
	if len(o.OrderBy) > 0 {
		opts = append(opts, WithOrderBy(o.OrderBy...))
	}
	if len(o.GroupBy) > 0 {
		opts = append(opts, WithGroupBy(o.GroupBy...))
	}
	if o.Limit != nil {
		opts = append(opts, WithLimit(*o.Limit))
	}
	if o.Offset != nil {
		opts = append(opts, WithOffset(*o.Offset))
	}
	if len(o.Select) > 0 {
		opts = append(opts, WithSelect(o.Select...))
	}
	for _, j := range o.Joins {
		opts = append(opts, WithJoin(justOrDefault(strings.ToUpper(j.Kind), "JOIN"), j.Table, j.On))
	}
	return opts
}

func justOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
