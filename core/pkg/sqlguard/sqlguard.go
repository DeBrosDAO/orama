// Package sqlguard is the filter between tenant-written SQL and the platform's
// own tables.
//
// Two paths hand a tenant's SQL to a namespace gateway's database handle: a
// function's db_query and db_execute host calls, and the raw-database routes
// of the ORM gateway (/v1/rqlite/exec, query, transaction, select, find,
// create-table and drop-table). On a namespace gateway that handle points at
// the namespace's rqlite — which is both the tenant's application database and
// the database that authenticates the namespace. The core migrations run
// there, so tables such as principals, grants, function_secrets and
// ipfs_content_ownership sit in the same schema as the tenant's own tables, and
// tenant SQL could read and write all of them.
//
// That is not a hole in one query; it is one database doing two jobs. Until the
// platform's own state lives somewhere a tenant's SQL cannot name, the only
// enforcement point is the statement itself, so that is where this guard is.
// It is deliberately conservative: it refuses a statement that mentions a
// protected name anywhere, rather than trying to work out whether that mention
// would have read anything.
//
// What it cannot do is worth stating plainly. A view or trigger created before
// this guard existed can still reach a protected table when queried by its own
// name, because the statement doing the querying does not mention the protected
// table at all. Separating platform state from tenant data is the fix; this
// closes the direct path in the meantime.
package sqlguard

import (
	"fmt"
	"strings"
)

// protectedTables are the core tables tenant SQL may not name.
//
// The list is not "every table the core migrations create". Several of those
// have generic names a tenant may already be using as their own — a namespace
// database has exactly one table called `apps`, and it belongs to whoever wrote
// to it first — so denying all of them would break working applications. What
// is denied is what grants authority, holds a credential, or configures the
// platform.
var protectedTables = map[string]string{
	// Credentials and identity. Writing any of these mints authority.
	"api_keys":        "authentication",
	"wallet_api_keys": "authentication",
	"refresh_tokens":  "authentication",
	"nonces":          "authentication",
	// A pending device login. Reading one hands out the code that collects
	// somebody else's session; writing one approves it.
	"device_authorizations": "pending logins",
	// Which devices hold sessions. Writing one activates a device or
	// un-revokes a tombstoned one.
	"session_devices": "which devices may hold a session",
	// Lowering it lets a wallet signature alone enrol a device.
	"namespace_session_policy": "what a sign-in must prove",
	"invite_tokens":            "cluster membership",
	"operators":                "operator identity",
	"cluster_settings":         "who may create namespaces, and how many",
	"namespace_creators":       "wallets allowed to create a namespace",
	"principals":               "who the platform will authenticate",
	// Public keys, but writing one publishes a key the cluster will accept
	// tokens from — which is minting authority by another route.
	"signing_keys": "which keys may sign a token",
	// Public keys too, but writing one is deciding which machine the cluster
	// will accept as a node, and deleting a row un-revokes a retired one.
	"node_credentials": "which key the cluster accepts as a node",
	"encryption_roots": "the IKM stored secrets are derived from",
	"grants":           "who may do what in a namespace",
	// 0.122.x's ownership table, kept while 0.122.x gateways still read it
	// during the rolling upgrade (migration 050 is expand-only).
	"namespace_ownership": "who owns a namespace (0.122.x)",
	// Rewriting it would move which window keys the contract release revokes.
	"api_keys_expiry_cutoff":     "which API keys the expiry backfill covers",
	"wireguard_peers":            "mesh membership and node agent tokens",
	"namespace_push_credentials": "push credentials",
	// A topic row is only ever written by whoever holds the topic's secret, and
	// its rows say nothing about which account a device belongs to (FEAT-265).
	// SQL that could write one re-points a topic without the secret; SQL that
	// could read one could join it against the caller of each invocation.
	"push_topics":       "push registrations addressed by rotating topic",
	"function_secrets":  "every function's secrets",
	"function_env_vars": "every function's environment",
	// Deleting a row here un-revokes a credential somebody revoked.
	"revoked_tokens": "which tokens are refused",
	// The record of who was given what and when. A record its own subject can
	// delete is not a record.
	"audit_events": "the audit trail",
	// What the public status page says the network's uptime was. A row a
	// tenant could write is a published record anyone could falsify.
	"status_uptime_hourly": "the public uptime record",

	// Platform limits. Writing these lifts the caller's own ceilings.
	"namespace_quotas":            "storage and resource quotas",
	"namespace_rate_limit_config": "rate limits",

	// Cluster topology and naming. Writing these redirects the platform.
	"namespace_clusters":           "cluster topology",
	"namespace_cluster_nodes":      "cluster topology",
	"namespace_port_allocations":   "port allocation",
	"global_deployment_subdomains": "subdomain ownership",
	"dns_records":                  "DNS",
	"dns_nodes":                    "DNS",
	"dns_nameservers":              "DNS",
	"raft_evicted_nodes":           "cluster membership",
	"cluster_locks":                "cluster coordination",
	"orama_schema_migrations":      "the platform's own schema bookkeeping",

	// Which namespace is which. API-key authentication resolves a key's
	// namespace by joining this table, so renaming a row hands one tenant's
	// keys another tenant's namespace (bugboard #427).
	"namespaces": "namespace identity",
	// Which namespace may read which CID. /v1/storage/get serves a CID to any
	// namespace holding a row here, decrypted with the cluster-wide wrap key,
	// so writing one is reading another tenant's content (bugboard #431).
	"ipfs_content_ownership": "which namespace may read stored content",
	// What runs where, and on which port. The gateway reads these rows to start,
	// route to and health-check a deployment, and a deployment's row names its
	// content CID, its entry point and its environment. A tenant deploys through
	// /v1/deployments, which validates all of that; a row written here skips it,
	// so it would run content the deploy path refused, on a port and a node the
	// tenant chose. No documented tenant workflow runs SQL against them.
	"deployments":           "what runs, from which content, on which port",
	"deployment_domains":    "which host names route to which deployment",
	"deployment_replicas":   "which nodes run a deployment",
	"home_node_assignments": "which node hosts a deployment",
	"port_allocations":      "which ports a deployment holds",
	// The cluster-wide reference count that decides whether an unpin removes a
	// shared pin. It lives in the cluster registry only, but a caller should
	// be told what it is rather than "no such table".
	"ipfs_cid_refs": "the cluster-wide count of who references stored content",

	// What a function is, and what fires it. A cron or pubsub firing skips the
	// caller check (docs/SECURITY.md), so a trigger row a tenant could write is
	// a way to run a private or internal function nobody granted it. A
	// `functions` row is the code and the visibility that decide who may
	// invoke it; a deploy through /v1/functions validates both.
	"functions":                "which function runs, its code and who may invoke it",
	"function_cron_triggers":   "which function a schedule fires, without a caller check",
	"function_pubsub_triggers": "which function a topic fires, without a caller check",
	"function_db_triggers":     "which function a database change fires",
	// Rollback reads it to pick the CID a deployment is restored to, so a
	// forged row rolls a deployment back to content of the caller's choosing.
	"deployment_history": "which content a rollback restores",
	// Where the platform's own per-namespace configuration is kept. Each is
	// written by a handler that validates it; a row written here skips that.
	"namespace_push_config":      "where and how a namespace's push is delivered",
	"namespace_webrtc_config":    "the TURN shared secret of a namespace",
	"namespace_sqlite_databases": "which tenant SQLite files exist and where",
	"namespace_sqlite_backups":   "which backup belongs to which tenant SQLite file",
	// Nothing on a namespace gateway reads these: the cluster manager owns
	// them, in the registry, and a reserved name says so rather than "no such
	// table".
	"webrtc_rooms":              "which SFU hosts a room",
	"webrtc_port_allocations":   "which node runs which WebRTC role",
	"namespace_cluster_events":  "cluster topology history",
	"namespace_pending_cleanup": "cleanup the cluster still owes a node",
	"node_health_events":        "what the cluster's nodes reported about each other",
	"rqlite_backups":            "where the registry's off-box backups are",
}

// deniedStatements are statement kinds tenant SQL has no use for and that step
// outside the database it was given: ATTACH reaches another database file,
// PRAGMA reads and changes engine state, VACUUM INTO writes a copy of the whole
// database to a path of the caller's choosing.
var deniedStatements = map[string]string{
	"attach": "opens another database",
	"detach": "detaches a database",
	"pragma": "reads or changes database engine state",
	"vacuum": "can write a copy of the whole database elsewhere",
}

// rawStorageTables are SQLite virtual tables that read the database file below
// the table level. `sqlite_dbpage` returns raw pages, protected tables'
// contents included, without the statement ever naming one; `dbstat` exposes
// page-level layout. Whether they exist depends on how SQLite was compiled, so
// they are refused by name rather than trusted to be absent.
var rawStorageTables = map[string]bool{
	"sqlite_dbpage": true,
	"dbstat":        true,
}

// ErrNotAllowed is what a refused statement returns. Callers tell a refusal
// from a database failure by errors.As on it.
type ErrNotAllowed struct {
	Reason string
}

func (e *ErrNotAllowed) Error() string { return e.Reason }

// Check refuses a statement tenant SQL may not run. It reads one statement: a
// caller with several checks each of them.
func Check(query string) error {
	tokens := tokenizeSQL(query)
	if isCreateTrigger(tokens) {
		return &ErrNotAllowed{Reason: "CREATE TRIGGER is not available to tenant SQL: a trigger runs statements the guard never sees"}
	}

	seenStatement := false
	for i, tok := range tokens {
		switch tok.kind {
		case tokenSemicolon:
			// Everything after the first statement's end is a second
			// statement. One host call runs one statement; a trailing
			// semicolon with nothing after it is fine.
			if hasMoreContent(tokens[i+1:]) {
				return &ErrNotAllowed{Reason: "one call runs one statement; send them one at a time"}
			}
		case tokenWord:
			if !seenStatement {
				seenStatement = true
				if why, denied := deniedStatements[strings.ToLower(tok.text)]; denied {
					return &ErrNotAllowed{
						Reason: fmt.Sprintf("%s is not available to tenant SQL: it %s", strings.ToUpper(tok.text), why),
					}
				}
			}
			if err := refuseProtected(tok.text); err != nil {
				return err
			}
		case tokenQuotedIdent:
			if err := refuseProtected(tok.text); err != nil {
				return err
			}
		case tokenString:
			if err := refuseProtectedLiteral(tok.text); err != nil {
				return err
			}
		}
	}
	return nil
}

// refuseProtected refuses an identifier that is a platform table's name, in
// any role — table, column, alias or named parameter. A role is not tracked:
// the name is reserved.
func refuseProtected(name string) error {
	n := normalizeName(name)
	if why, protected := protectedTables[n]; protected {
		return &ErrNotAllowed{Reason: fmt.Sprintf(
			"the name %s is reserved for a platform table (%s) and may not appear in tenant SQL, as a table, column, alias or parameter name", n, why)}
	}
	if rawStorageTables[n] {
		return &ErrNotAllowed{Reason: fmt.Sprintf("%s reads the database file below the table level and is not available to tenant SQL", n)}
	}
	return nil
}

// refuseProtectedLiteral refuses a string literal whose whole text is a
// platform table's name. SQLite's grammar accepts a string literal wherever
// it takes a name, so `FROM 'api_keys'`, `FROM messages, 'api_keys'`,
// `FROM ('api_keys')` and `UPDATE OR REPLACE 'api_keys'` all reach the table.
// Tracking which positions those are meant re-implementing SQLite's grammar,
// and every list of them missed one (bugboard #425). So the literal is refused
// wherever it appears. A caller that needs that text as data binds it as an
// argument, which never reaches the SQL text at all.
func refuseProtectedLiteral(text string) error {
	n := normalizeName(text)
	if _, protected := protectedTables[n]; protected || rawStorageTables[n] {
		return &ErrNotAllowed{Reason: fmt.Sprintf(
			"the string literal '%s' is the name of a platform table, and SQLite reads a string literal as a table name in many positions; pass the value as a bound argument (?) instead", n)}
	}
	return nil
}

// normalizeName folds a name for comparison. SQLite folds ASCII case only;
// strings.ToLower and TrimSpace also fold and trim Unicode, a deliberate
// superset that can only make the guard refuse more.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// isCreateTrigger reports whether the statement so far is CREATE [TEMP|
// TEMPORARY] TRIGGER. A trigger body is statements run later by SQLite on its
// own, so nothing a tenant sends may create one. (Every trigger body today
// also contains a ';', which the one-statement rule refuses; this does not
// rely on that.)
func isCreateTrigger(tokens []token) bool {
	var words []string
	for _, tok := range tokens {
		if tok.kind != tokenWord || len(words) == 3 {
			break
		}
		words = append(words, strings.ToLower(tok.text))
	}
	if len(words) < 2 || words[0] != "create" {
		return false
	}
	if words[1] == "trigger" {
		return true
	}
	return len(words) == 3 && (words[1] == "temp" || words[1] == "temporary") && words[2] == "trigger"
}

func hasMoreContent(tokens []token) bool {
	for _, tok := range tokens {
		if tok.kind != tokenSemicolon {
			return true
		}
	}
	return false
}

type tokenKind int

const (
	tokenWord tokenKind = iota
	tokenQuotedIdent
	tokenString
	tokenSemicolon
	tokenDot
	tokenOther
)

type token struct {
	kind tokenKind
	text string
}

// tokenizeSQL splits a statement into the pieces the guard cares about:
// identifiers however they are quoted, string literals, semicolons and dots.
// Comments are discarded, so a name cannot be hidden inside one.
func tokenizeSQL(q string) []token {
	var out []token
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			i += 2
			for i+1 < len(q) && !(q[i] == '*' && q[i+1] == '/') {
				i++
			}
			if i+1 < len(q) {
				i += 2
			} else {
				i = len(q)
			}
		case c == '\'':
			text, next := readDelimited(q, i, '\'', '\'')
			out = append(out, token{kind: tokenString, text: text})
			i = next
		case c == '"':
			text, next := readDelimited(q, i, '"', '"')
			out = append(out, token{kind: tokenQuotedIdent, text: text})
			i = next
		case c == '`':
			text, next := readDelimited(q, i, '`', '`')
			out = append(out, token{kind: tokenQuotedIdent, text: text})
			i = next
		case c == '[':
			text, next := readDelimited(q, i, '[', ']')
			out = append(out, token{kind: tokenQuotedIdent, text: text})
			i = next
		case c == ';':
			out = append(out, token{kind: tokenSemicolon, text: ";"})
			i++
		case c == '.':
			out = append(out, token{kind: tokenDot, text: "."})
			i++
		case isSQLiteSpace(c):
			// Whitespace separates tokens and is not one. Keeping it would
			// put a space between `FROM` and the name that follows it.
			i++
		case isWordByte(c):
			start := i
			for i < len(q) && isWordByte(q[i]) {
				i++
			}
			out = append(out, token{kind: tokenWord, text: q[start:i]})
		default:
			out = append(out, token{kind: tokenOther, text: string(c)})
			i++
		}
	}
	return out
}

// readDelimited reads a quoted run starting at i, where a doubled closing
// delimiter is an escaped one (SQLite's rule for ” and ""). It returns the
// contents and the index just past the closing delimiter.
func readDelimited(q string, i int, open, close byte) (string, int) {
	var b strings.Builder
	i++ // past the opening delimiter
	for i < len(q) {
		if q[i] == close {
			if open != '[' && i+1 < len(q) && q[i+1] == close {
				b.WriteByte(close)
				i += 2
				continue
			}
			return b.String(), i + 1
		}
		b.WriteByte(q[i])
		i++
	}
	// Unterminated. Return what there is; an unterminated quote is a syntax
	// error the database will reject, and the guard has still seen the name.
	return b.String(), i
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isSQLiteSpace is SQLite's whitespace as the guard must see it: the bytes
// its tokenizer accepts between tokens (space, \t, \n, \f, \r), plus \v,
// which SQLite skips once a run of whitespace has begun. Missing one let
// `FROM\f'api_keys'` read to the guard as something other than FROM followed
// by a name (bugboard #425). The set is a deliberate superset: \v where SQLite
// would start a token is a syntax error there anyway.
func isSQLiteSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == '\v'
}
