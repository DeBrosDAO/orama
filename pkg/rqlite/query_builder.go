package rqlite

// query_builder.go implements a fluent SQL query builder for SELECT statements.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// QueryBuilder implements a fluent SELECT builder with joins, where, etc.
type QueryBuilder struct {
	exec    executor
	table   string
	alias   string
	selects []string

	joins  []joinClause
	wheres []whereClause

	groupBys []string
	orderBys []string
	limit    *int
	offset   *int
}

// joinClause represents INNER/LEFT/etc joins.
type joinClause struct {
	kind  string // "INNER", "LEFT", "JOIN" (default)
	table string
	on    string
}

// whereClause holds an expression and args with a conjunction.
type whereClause struct {
	conj string // "AND" or "OR"
	expr string
	args []any
}

// newQueryBuilder creates a new QueryBuilder for the given table.
func newQueryBuilder(exec executor, table string) *QueryBuilder {
	return &QueryBuilder{
		exec:  exec,
		table: table,
	}
}

// Select specifies columns to select.
func (qb *QueryBuilder) Select(cols ...string) *QueryBuilder {
	qb.selects = append(qb.selects, cols...)
	return qb
}

// Alias sets an alias for the main table.
func (qb *QueryBuilder) Alias(a string) *QueryBuilder {
	qb.alias = a
	return qb
}

// Where adds a WHERE clause (same as AndWhere).
func (qb *QueryBuilder) Where(expr string, args ...any) *QueryBuilder {
	return qb.AndWhere(expr, args...)
}

// AndWhere adds an AND WHERE clause.
func (qb *QueryBuilder) AndWhere(expr string, args ...any) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{conj: "AND", expr: expr, args: args})
	return qb
}

// OrWhere adds an OR WHERE clause.
func (qb *QueryBuilder) OrWhere(expr string, args ...any) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{conj: "OR", expr: expr, args: args})
	return qb
}

// InnerJoin adds an INNER JOIN clause.
func (qb *QueryBuilder) InnerJoin(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "INNER", table: table, on: on})
	return qb
}

// LeftJoin adds a LEFT JOIN clause.
func (qb *QueryBuilder) LeftJoin(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "LEFT", table: table, on: on})
	return qb
}

// Join adds a JOIN clause.
func (qb *QueryBuilder) Join(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "JOIN", table: table, on: on})
	return qb
}

// GroupBy adds GROUP BY columns.
func (qb *QueryBuilder) GroupBy(cols ...string) *QueryBuilder {
	qb.groupBys = append(qb.groupBys, cols...)
	return qb
}

// OrderBy adds ORDER BY expressions.
func (qb *QueryBuilder) OrderBy(exprs ...string) *QueryBuilder {
	qb.orderBys = append(qb.orderBys, exprs...)
	return qb
}

// Limit sets the LIMIT clause.
func (qb *QueryBuilder) Limit(n int) *QueryBuilder {
	qb.limit = &n
	return qb
}

// Offset sets the OFFSET clause.
func (qb *QueryBuilder) Offset(n int) *QueryBuilder {
	qb.offset = &n
	return qb
}

// Build returns the SQL string and args for a SELECT.
func (qb *QueryBuilder) Build() (string, []any) {
	cols := "*"
	if len(qb.selects) > 0 {
		cols = strings.Join(qb.selects, ", ")
	}
	base := fmt.Sprintf("SELECT %s FROM %s", cols, qb.table)
	if qb.alias != "" {
		base += " AS " + qb.alias
	}

	args := make([]any, 0, 16)
	for _, j := range qb.joins {
		base += fmt.Sprintf(" %s JOIN %s ON %s", j.kind, j.table, j.on)
	}

	if len(qb.wheres) > 0 {
		base += " WHERE "
		for i, w := range qb.wheres {
			if i > 0 {
				base += " " + w.conj + " "
			}
			base += "(" + w.expr + ")"
			args = append(args, w.args...)
		}
	}

	if len(qb.groupBys) > 0 {
		base += " GROUP BY " + strings.Join(qb.groupBys, ", ")
	}
	if len(qb.orderBys) > 0 {
		base += " ORDER BY " + strings.Join(qb.orderBys, ", ")
	}
	if qb.limit != nil {
		base += fmt.Sprintf(" LIMIT %d", *qb.limit)
	}
	if qb.offset != nil {
		base += fmt.Sprintf(" OFFSET %d", *qb.offset)
	}
	return base, args
}

// GetMany executes the built query and scans into dest (pointer to slice).
func (qb *QueryBuilder) GetMany(ctx context.Context, dest any) error {
	sqlStr, args := qb.Build()
	rows, err := qb.exec.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// GetOne executes the built query and scans into dest (pointer to struct or map) with LIMIT 1.
func (qb *QueryBuilder) GetOne(ctx context.Context, dest any) error {
	limit := 1
	if qb.limit == nil {
		qb.limit = &limit
	} else if qb.limit != nil && *qb.limit > 1 {
		qb.limit = &limit
	}
	sqlStr, args := qb.Build()
	rows, err := qb.exec.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return sql.ErrNoRows
	}
	return scanIntoSingle(rows, dest)
}
