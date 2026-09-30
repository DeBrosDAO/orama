package rqlite

import (
	"fmt"
	"net/http"
	"regexp"
)

// CodeSQLNotAllowed is the code on the 403 a guarded gateway answers with when
// a request's SQL names something the guard refuses.
const CodeSQLNotAllowed = "SQL_NOT_ALLOWED"

// SQLGuard inspects one SQL statement before the gateway runs it and returns an
// error to refuse it. It is a function, not an import, so this package need not
// know what the caller protects: the gateway package installs
// sqlguard.Check on a namespace gateway, where the database holds the platform's
// own tables beside the tenant's, and leaves it nil on the cluster gateway,
// whose raw-database routes are already an operator's.
type SQLGuard func(sql string) error

// criteriaColumnRe is what a find criteria key must be while a guard is set: a
// column name, optionally qualified. FindBy writes the key into the statement
// as `<key> = ?`, so any other key is SQL. The guard reads each statement as
// built; a key holding a quote or a comment marker could change what the text
// around it means, and map order would decide which text that is.
var criteriaColumnRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// checkSQL runs the guard over one statement. Without a guard everything is
// allowed.
func (g *HTTPGateway) checkSQL(sql string) error {
	if g.SQLGuard == nil {
		return nil
	}
	return g.SQLGuard(sql)
}

// checkFind guards the statement FindBy or FindOneBy would build for a find
// request, without running it.
func (g *HTTPGateway) checkFind(table string, criteria map[string]any, opts []FindOption) error {
	if g.SQLGuard == nil {
		return nil
	}
	qb := newQueryBuilder(nil, table)
	for k, v := range criteria {
		if !criteriaColumnRe.MatchString(k) {
			return fmt.Errorf("criteria key %q is not a plain column name", k)
		}
		qb = qb.AndWhere(k+" = ?", v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return g.checkBuilt(qb)
}

// checkBuilt guards the statement a query builder would run.
func (g *HTTPGateway) checkBuilt(qb *QueryBuilder) error {
	if g.SQLGuard == nil {
		return nil
	}
	stmt, _ := qb.Build()
	return g.SQLGuard(stmt)
}

// refuseSQL answers a request whose SQL the guard refused.
func refuseSQL(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": err.Error(),
		"code":  CodeSQLNotAllowed,
	})
}
