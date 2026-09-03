package rqlite

// client.go provides the main ORM-like client that coordinates all components.
// It builds on the rqlite stdlib driver to behave like a regular SQL-backed ORM.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rqlite/gorqlite"
)

// freshReadHTTPTimeout caps a single native none+freshness read (PART B,
// queryNoneFresh). Matches the 5s status-query timeout used elsewhere; a
// freshness-bound local read should complete in single-digit ms.
const freshReadHTTPTimeout = 5 * time.Second

// NewClient wires the ORM client to a *sql.DB (from your RQLiteAdapter).
//
// The client constructed here can do everything EXCEPT atomic Batch — that
// requires the native gorqlite connection, which has no path through
// database/sql. Use NewClientWithDSN or NewClientWithConn if you need Batch.
func NewClient(db *sql.DB) Client {
	return &client{db: db}
}

// NewClientWithDSN wires the ORM client to BOTH a *sql.DB (for Query/Exec) and
// a native *gorqlite.Connection (for Batch atomicity).
//
// The DSN must be the standard rqlite connection URL ("http://user:pass@host:port"
// or "https://..."). Both connections share configuration but are independent
// HTTP clients.
//
// It also opens a SECOND native connection pinned to level=none, used by the
// opt-in local-read path (BatchQueryConsistency). gorqlite's consistency level
// is per-connection, not per-query, so a dedicated connection is the only way
// to offer none-level reads without disturbing the default weak reads.
//
// Returns an error if either gorqlite native dial fails. The *sql.DB is not
// validated here — callers should already have done that.
func NewClientWithDSN(db *sql.DB, dsn string) (Client, error) {
	conn, err := gorqlite.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("rqlite.NewClientWithDSN: native dial failed: %s", RedactError(err, dsn))
	}
	connNone, err := gorqlite.Open(dsn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rqlite.NewClientWithDSN: native dial (none-level) failed: %s", RedactError(err, dsn))
	}
	if err := connNone.SetConsistencyLevel(gorqlite.ConsistencyLevelNone); err != nil {
		conn.Close()
		connNone.Close()
		return nil, fmt.Errorf("rqlite.NewClientWithDSN: pin none consistency: %w", err)
	}
	c := &client{db: db, conn: conn, connNone: connNone}
	// Parse the DSN once to wire (a) the local-follower freshness gate (#1022)
	// and (b) the native none+freshness read path (#1021). A DSN that doesn't
	// parse leaves both disabled (gate nil, freshHTTP nil) rather than failing
	// construction — none-reads then behave exactly as before this change.
	if parts, ok := parseDSNParts(dsn); ok {
		c.localStatusPort = parts.port
		c.staleGate = newFollowerFreshnessGate(parts.port, LocalFollowerFresh, 0)
		c.freshScheme = parts.scheme
		c.freshHost = parts.host
		c.freshUser = parts.user
		c.freshPass = parts.pass
		c.freshHTTP = &http.Client{Timeout: freshReadHTTPTimeout}
	}
	return c, nil
}

// dsnParts holds the components of a parsed rqlite DSN needed by the freshness
// gate and the native none+freshness read path.
type dsnParts struct {
	scheme string
	host   string // host:port (the serving node we POST reads to)
	port   int    // numeric port for the local /status query
	user   string
	pass   string
}

// parseDSNParts parses a standard rqlite DSN ("http://user:pass@host:port")
// into its components. Returns ok=false (and the caller leaves the gate/HTTP
// path disabled) when the DSN can't be parsed or lacks a numeric port — never
// an error, so construction stays backward compatible.
func parseDSNParts(dsn string) (dsnParts, bool) {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return dsnParts{}, false
	}
	portStr := u.Port()
	if portStr == "" {
		return dsnParts{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return dsnParts{}, false
	}
	parts := dsnParts{scheme: u.Scheme, host: u.Host, port: port}
	if u.User != nil {
		parts.user = u.User.Username()
		parts.pass, _ = u.User.Password()
	}
	return parts, true
}

// RedactDSN returns dsn with the URL password replaced by "REDACTED".
// Unparseable strings are returned unchanged except for a user:pass@ pattern.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return redactUserinfo(dsn)
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}

// RedactError returns err.Error() with the DSN and its password stripped.
// The original error is not wrapped: gorqlite embeds the DSN in the message.
func RedactError(err error, dsn string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, RedactDSN(dsn))
	}
	if u, e := url.Parse(dsn); e == nil && u.User != nil {
		if pass, ok := u.User.Password(); ok && pass != "" {
			msg = strings.ReplaceAll(msg, pass, "REDACTED")
		}
	}
	return msg
}

func redactUserinfo(dsn string) string {
	// Strip the first user:pass@ if Parse failed (malformed DSN is the leak path).
	at := strings.Index(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at <= 0 || scheme < 0 || at < scheme {
		return dsn
	}
	rest := dsn[scheme+3 : at]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return dsn
	}
	return dsn[:scheme+3] + rest[:colon] + ":REDACTED" + dsn[at:]
}

// NewClientWithConn wires the ORM client when the caller already has a
// *gorqlite.Connection. Useful when reusing the connection from RQLiteManager.
func NewClientWithConn(db *sql.DB, conn *gorqlite.Connection) Client {
	return &client{db: db, conn: conn}
}

// NewClientFromAdapter is convenient if you already created the adapter.
// Note: Batch is unavailable on this client; use the DSN/Conn constructors
// when atomicity matters.
func NewClientFromAdapter(adapter *RQLiteAdapter) Client {
	return NewClient(adapter.GetSQLDB())
}

// client implements Client over *sql.DB plus an optional *gorqlite.Connection
// for the atomic Batch path. When conn is nil, Batch returns an error.
type client struct {
	db   *sql.DB
	conn *gorqlite.Connection
	// connNone is a second native connection pinned to level=none. Used only
	// by BatchQueryConsistency(ReadConsistencyNone) for fast LOCAL reads that
	// skip the leader hop. nil for clients built without a native connection
	// (NewClient) or via NewClientWithConn — in which case none-reads degrade
	// to the weak conn (always correct, just slower).
	connNone *gorqlite.Connection

	// localStatusPort is the rqlite HTTP port of the LOCAL serving node, parsed
	// from the DSN. Used by the freshness gate to query /status. Zero when the
	// DSN had no usable port (gate disabled).
	localStatusPort int
	// staleGate gates none-reads on local-follower freshness (#1022). nil
	// disables gating (NewClient / NewClientWithConn, or an unparseable DSN) —
	// none-reads then behave exactly as before this change.
	staleGate *followerFreshnessGate

	// fresh* fields support the native none+freshness read path (#1021,
	// freshness_read.go). They are populated from the DSN in NewClientWithDSN;
	// freshHTTP nil means the path is unavailable and BatchQueryFresh with a
	// positive freshness errors out. freshPass is NEVER logged.
	freshScheme string
	freshHost   string
	freshUser   string
	freshPass   string
	freshHTTP   *http.Client
}

// ReadConsistency selects the rqlite read-consistency level for a read path.
// rqlite consistency applies to READS only; writes always traverse Raft.
//
//   - ReadConsistencyWeak (default): the serving node forwards the read to the
//     leader, so it always observes the latest committed write. On a
//     cross-region cluster this costs a full leader round-trip per read
//     (feat-6: ~273ms on the Singapore↔leader hop).
//   - ReadConsistencyNone: the serving node answers from its LOCAL SQLite
//     without contacting the leader (~1ms). It may return a slightly stale
//     snapshot when this node is a follower lagging in Raft replay, so it is
//     ONLY safe for reads that do not need to observe a write made earlier in
//     the same invocation (bug #235). Read-your-own-writes flows must stay on
//     weak, or fold the read into a DBTransaction post-commit query.
type ReadConsistency string

const (
	ReadConsistencyWeak ReadConsistency = "weak"
	ReadConsistencyNone ReadConsistency = "none"
)

// useNoneConn reports whether a read at consistency rc should use the
// dedicated none-level connection. Pure decision split out for unit testing
// without a live rqlite dial.
func useNoneConn(rc ReadConsistency, hasNoneConn bool) bool {
	return rc == ReadConsistencyNone && hasNoneConn
}

// queryConn picks the native connection matching the requested read
// consistency. Returns the weak (leader-routed) connection when none-level is
// not requested or not available; weak is always correct, only slower.
func (c *client) queryConn(rc ReadConsistency) *gorqlite.Connection {
	if useNoneConn(rc, c.connNone != nil) {
		return c.connNone
	}
	return c.conn
}

// Query runs an arbitrary SELECT and scans rows into dest.
// Query runs a SELECT and scans results into dest.
// Includes panic recovery because the gorqlite stdlib driver can panic
// with "index out of range" when RQLite is temporarily unavailable.
func (c *client) Query(ctx context.Context, dest any, query string, args ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (QueryContext): %v", r)
		}
	}()
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// Exec runs a write statement (INSERT/UPDATE/DELETE).
// Includes panic recovery because the gorqlite stdlib driver can panic
// with "index out of range" when RQLite is temporarily unavailable.
func (c *client) Exec(ctx context.Context, query string, args ...any) (result sql.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (ExecContext): %v", r)
		}
	}()
	return c.db.ExecContext(ctx, query, args...)
}

// FindBy finds entities matching criteria using simple map-based filtering.
func (c *client) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetMany(ctx, dest)
}

// FindOneBy finds a single entity matching criteria.
func (c *client) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetOne(ctx, dest)
}

// Save inserts or updates an entity based on primary key value.
func (c *client) Save(ctx context.Context, entity any) error {
	return saveEntity(ctx, c.db, entity)
}

// Remove deletes an entity by primary key.
func (c *client) Remove(ctx context.Context, entity any) error {
	return removeEntity(ctx, c.db, entity)
}

// Repository returns a typed repository for a table.
// Note: Returns untyped interface - users must type assert to Repository[T].
func (c *client) Repository(table string) any {
	return func() any {
		return &repository[any]{c: c, table: table}
	}()
}

// CreateQueryBuilder creates a fluent query builder for advanced querying.
func (c *client) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(c.db, table)
}

// Tx executes a function within a transaction.
func (c *client) Tx(ctx context.Context, fn func(tx Tx) error) error {
	sqlTx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txc := &txClient{tx: sqlTx}
	if err := fn(txc); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}
