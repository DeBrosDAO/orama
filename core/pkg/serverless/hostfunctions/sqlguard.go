package hostfunctions

import (
	"fmt"
	"strings"
)

// A function's db_query and db_execute hand the guest's SQL to the gateway's
// own database handle. On a namespace gateway that handle points at the
// namespace's rqlite — which is both the tenant's application database and the
// database that authenticates the namespace. The core migrations run there, so
// api_keys, namespace_ownership, refresh_tokens, nonces, operators,
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
	"api_keys":                   "authentication",
	"wallet_api_keys":            "authentication",
	"refresh_tokens":             "authentication",
	"nonces":                     "authentication",
	"invite_tokens":              "cluster membership",
	"operators":                  "operator identity",
	"namespace_ownership":        "namespace ownership",
	"wireguard_peers":            "mesh membership and node agent tokens",
	"namespace_push_credentials": "push credentials",
	"function_secrets":           "every function's secrets",
	"function_env_vars":          "every function's environment",
	// Deleting a row here un-revokes a credential somebody revoked.
	"revoked_tokens": "which tokens are refused",

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
	"reserved_domains":             "domain reservation",
	"raft_evicted_nodes":           "cluster membership",
	"cluster_locks":                "cluster coordination",
	"orama_schema_migrations":      "the platform's own schema bookkeeping",
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

// ErrSQLNotAllowed is what a refused statement returns.
type ErrSQLNotAllowed struct {
	Reason string
}

func (e *ErrSQLNotAllowed) Error() string { return e.Reason }

// checkGuestSQL refuses a statement a function may not run.
func checkGuestSQL(query string) error {
	tokens := tokenizeSQL(query)

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
			// SQLite accepts a string literal where a table name is
			// expected, so `FROM 'api_keys'` reads the table. Only a
			// string in that position is treated as a name; everywhere
			// else a string is the tenant's own data and is left alone.
			if i > 0 && precedesATableName(tokens[:i]) {
				if err := refuseProtected(tok.text); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func refuseProtected(name string) error {
	if why, protected := protectedTables[strings.ToLower(strings.TrimSpace(name))]; protected {
		return &ErrSQLNotAllowed{
			Reason: fmt.Sprintf("%s belongs to the platform (%s) and is not readable or writable from a function", strings.ToLower(name), why),
		}
	}
	return nil
}

// tableNameKeywords are the words a table name may directly follow. They are
// what makes a string literal in that spot a name rather than data.
var tableNameKeywords = map[string]bool{
	"from": true, "join": true, "into": true, "update": true,
	"table": true, "on": true, "exists": true,
}

// precedesATableName reports whether the tokens before a string literal put it
// where a table name goes, stepping back over a schema qualifier so
// `FROM main.'api_keys'` and `FROM "main"."api_keys"` are read the same way as
// `FROM api_keys`.
func precedesATableName(before []token) bool {
	i := len(before) - 1
	for i >= 0 && before[i].kind == tokenDot {
		i-- // the qualifier itself
		if i < 0 {
			return false
		}
		switch before[i].kind {
		case tokenWord, tokenQuotedIdent, tokenString:
			i-- // whatever precedes the qualifier
		default:
			return false
		}
	}
	if i < 0 {
		return false
	}
	return before[i].kind == tokenWord && tableNameKeywords[strings.ToLower(before[i].text)]
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
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
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
