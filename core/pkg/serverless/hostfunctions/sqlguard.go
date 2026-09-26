package hostfunctions

import (
	"fmt"
	"strings"
)

// A function's db_query and db_execute hand the guest's SQL to the gateway's
// own database handle. On a namespace gateway that handle points at the
// namespace's rqlite — which is both the tenant's application database and the
// database that authenticates the namespace. The core migrations run there, so
// api_keys, principals, grants, refresh_tokens, nonces, operators,
// wireguard_peers and function_secrets sit in the same schema as the tenant's
// own tables, and a function could read and write all of them.
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

// protectedTables are the core tables a function's SQL may not name.
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
}

// deniedStatements are statement kinds a function has no use for and that step
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

// ErrSQLNotAllowed is what a refused statement returns.
type ErrSQLNotAllowed struct {
	Reason string
}

func (e *ErrSQLNotAllowed) Error() string { return e.Reason }

// checkGuestSQL refuses a statement a function may not run.
func checkGuestSQL(query string) error {
	tokens := tokenizeSQL(query)
	if isCreateTrigger(tokens) {
		return &ErrSQLNotAllowed{Reason: "CREATE TRIGGER is not available to a function: a trigger runs statements the guard never sees"}
	}

	seenStatement := false
	for i, tok := range tokens {
		switch tok.kind {
		case tokenSemicolon:
			// Everything after the first statement's end is a second
			// statement. One host call runs one statement; a trailing
			// semicolon with nothing after it is fine.
			if hasMoreContent(tokens[i+1:]) {
				return &ErrSQLNotAllowed{Reason: "a database host call runs one statement; send them one at a time"}
			}
		case tokenWord:
			if !seenStatement {
				seenStatement = true
				if why, denied := deniedStatements[strings.ToLower(tok.text)]; denied {
					return &ErrSQLNotAllowed{
						Reason: fmt.Sprintf("%s is not available to a function: it %s", strings.ToUpper(tok.text), why),
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
		return &ErrSQLNotAllowed{Reason: fmt.Sprintf(
			"the name %s is reserved for a platform table (%s) and may not appear in a function's SQL, as a table, column, alias or parameter name", n, why)}
	}
	if rawStorageTables[n] {
		return &ErrSQLNotAllowed{Reason: fmt.Sprintf("%s reads the database file below the table level and is not available to a function", n)}
	}
	return nil
}

// refuseProtectedLiteral refuses a string literal whose whole text is a
// platform table's name. SQLite's grammar accepts a string literal wherever
// it takes a name, so `FROM 'api_keys'`, `FROM messages, 'api_keys'`,
// `FROM ('api_keys')` and `UPDATE OR REPLACE 'api_keys'` all reach the table.
// Tracking which positions those are meant re-implementing SQLite's grammar,
// and every list of them missed one (bugboard #425). So the literal is refused
// wherever it appears. A function that needs that text as data binds it as an
// argument, which never reaches the SQL text at all.
func refuseProtectedLiteral(text string) error {
	n := normalizeName(text)
	if _, protected := protectedTables[n]; protected || rawStorageTables[n] {
		return &ErrSQLNotAllowed{Reason: fmt.Sprintf(
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
// own, so nothing a function sends may create one. (Every trigger body today
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
