package sqlguard

import (
	"fmt"
	"strings"
)

// scopedStatements are the statement kinds a scoped handle runs: data
// manipulation only. DDL, PRAGMA, ATTACH and VACUUM change or step outside the
// database, which no code holding a table-scoped handle has a reason to do.
var scopedStatements = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true,
	"replace": true, "with": true,
}

// reservedNamePrefixes are the SQLite schema and pragma surfaces. Reading
// sqlite_master names every table in the database, and a pragma_* table-valued
// function reads engine state, without the statement naming any table at all.
var reservedNamePrefixes = []string{"sqlite_", "pragma_"}

// CheckScope refuses a statement that reaches a table outside a handle's scope.
//
// Check protects the platform's tables from tenant SQL. This is the same
// technique pointed the other way, at the platform's own code: a component that
// is given a handle on a database holding many tables (the cluster registry)
// should be able to reach the tables it needs and no others, so that a bug that
// lets an attacker choose or alter one of its statements cannot be turned into
// a read of, or a write to, a table that component has no business with.
//
// known is every table in the database, allowed the ones the component may name.
// As in Check, a name is judged wherever it appears (table, column, alias,
// quoted identifier or string literal): the guard does not work out which role
// the name plays, so it can only refuse more than a grammar-aware one would, and
// a component whose own column shares a name with another table is refused and
// must rename it or bind the text as an argument. A name that is in neither set
// is not a table of this database and is not judged.
//
// One statement, of a data-manipulation kind, is read per call.
func CheckScope(query string, allowed, known map[string]bool) error {
	tokens := tokenizeSQL(query)
	if isCreateTrigger(tokens) {
		return &ErrNotAllowed{Reason: "CREATE TRIGGER is not available on a scoped handle"}
	}

	seenStatement := false
	for i, tok := range tokens {
		switch tok.kind {
		case tokenSemicolon:
			if hasMoreContent(tokens[i+1:]) {
				return &ErrNotAllowed{Reason: "one call runs one statement; send them one at a time"}
			}
		case tokenWord:
			if !seenStatement {
				seenStatement = true
				if !scopedStatements[normalizeName(tok.text)] {
					return &ErrNotAllowed{Reason: fmt.Sprintf(
						"%s is not available on a scoped handle: only SELECT, INSERT, UPDATE, DELETE and REPLACE are", strings.ToUpper(tok.text))}
				}
			}
			if err := checkScopedName(tok.text, allowed, known); err != nil {
				return err
			}
		case tokenQuotedIdent, tokenString:
			if err := checkScopedName(tok.text, allowed, known); err != nil {
				return err
			}
		}
	}
	if !seenStatement {
		return &ErrNotAllowed{Reason: "an empty statement is not available on a scoped handle"}
	}
	return nil
}

func checkScopedName(name string, allowed, known map[string]bool) error {
	n := normalizeName(name)
	for _, prefix := range reservedNamePrefixes {
		if strings.HasPrefix(n, prefix) {
			return &ErrNotAllowed{Reason: fmt.Sprintf("%s reads the database's own schema or engine state and is not available on a scoped handle", n)}
		}
	}
	if rawStorageTables[n] {
		return &ErrNotAllowed{Reason: fmt.Sprintf("%s reads the database file below the table level and is not available on a scoped handle", n)}
	}
	if known[n] && !allowed[n] {
		return &ErrNotAllowed{Reason: fmt.Sprintf("the table %s is outside this handle's scope", n)}
	}
	return nil
}
