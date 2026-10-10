package rqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrOutsideScope is returned by a GuardedClient method that is not a plain SQL
// statement (entity and repository calls, the sequenced batch), which a guard
// reading statement text cannot judge.
var ErrOutsideScope = errors.New("not available on a scoped database handle")

// GuardedClient is a Client that runs every statement through a SQLGuard first.
//
// It narrows what a component can reach on a database it has been handed whole.
// The guard is code in this process: it stops a statement the component should
// never issue, which is what a bug in the component's own SQL handling would
// produce. It does not stop code that already runs in this process and holds the
// connection's credentials, and it is not a database permission; see
// docs/whitepaper/technical-reference/vol1/17-database.md for what a scoped handle is and is not.
//
// Only the calls a guard can read are available: Query, Exec, Tx, Batch and
// BatchQuery. The entity layer (FindBy, Save, Remove, Repository,
// CreateQueryBuilder) builds its SQL from a table name or a struct, and
// BatchWithSeq writes a publish counter in a table no scoped component owns;
// those are refused rather than guarded.
type GuardedClient struct {
	inner Client
	guard SQLGuard
	scope string
}

// NewGuardedClient wraps inner so that guard judges each statement. scope names
// the component in the errors a refusal carries.
func NewGuardedClient(inner Client, scope string, guard SQLGuard) *GuardedClient {
	return &GuardedClient{inner: inner, guard: guard, scope: scope}
}

func (c *GuardedClient) check(query string) error {
	if err := c.guard(query); err != nil {
		return fmt.Errorf("%s: statement refused: %w", c.scope, err)
	}
	return nil
}

func (c *GuardedClient) refuse(call string) error {
	return fmt.Errorf("%s: %s is %w", c.scope, call, ErrOutsideScope)
}

// Query runs a guarded SELECT.
func (c *GuardedClient) Query(ctx context.Context, dest any, query string, args ...any) error {
	if err := c.check(query); err != nil {
		return err
	}
	return c.inner.Query(ctx, dest, query, args...)
}

// Exec runs a guarded write statement.
func (c *GuardedClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := c.check(query); err != nil {
		return nil, err
	}
	return c.inner.Exec(ctx, query, args...)
}

// FindBy is refused: see GuardedClient.
func (c *GuardedClient) FindBy(context.Context, any, string, map[string]any, ...FindOption) error {
	return c.refuse("FindBy")
}

// FindOneBy is refused: see GuardedClient.
func (c *GuardedClient) FindOneBy(context.Context, any, string, map[string]any, ...FindOption) error {
	return c.refuse("FindOneBy")
}

// Save is refused: see GuardedClient.
func (c *GuardedClient) Save(context.Context, any) error { return c.refuse("Save") }

// Remove is refused: see GuardedClient.
func (c *GuardedClient) Remove(context.Context, any) error { return c.refuse("Remove") }

// Repository returns nil: a repository is built from a struct, not a statement.
// A caller that asks for one gets a failed type assertion, not a way around the
// guard.
func (c *GuardedClient) Repository(string) any { return nil }

// CreateQueryBuilder returns a builder with no database behind it, so what it
// builds cannot run.
func (c *GuardedClient) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(nil, table)
}

// Tx runs fn in a transaction whose statements are guarded.
func (c *GuardedClient) Tx(ctx context.Context, fn func(tx Tx) error) error {
	return c.inner.Tx(ctx, func(tx Tx) error {
		return fn(&guardedTx{inner: tx, owner: c})
	})
}

// Batch guards every op, then runs the batch.
func (c *GuardedClient) Batch(ctx context.Context, ops []BatchOp) (*BatchResult, error) {
	if err := c.checkOps(ops); err != nil {
		return nil, err
	}
	return c.inner.Batch(ctx, ops)
}

// BatchWithSeq is refused: see GuardedClient.
func (c *GuardedClient) BatchWithSeq(context.Context, string, []BatchOp) (*BatchResult, int64, error) {
	return nil, 0, c.refuse("BatchWithSeq")
}

// BatchQuery guards every op, then runs the queries.
func (c *GuardedClient) BatchQuery(ctx context.Context, ops []BatchOp) ([]OpResult, error) {
	if err := c.checkOps(ops); err != nil {
		return nil, err
	}
	return c.inner.BatchQuery(ctx, ops)
}

func (c *GuardedClient) checkOps(ops []BatchOp) error {
	for i, op := range ops {
		if err := c.check(op.SQL); err != nil {
			return fmt.Errorf("batch op %d: %w", i, err)
		}
	}
	return nil
}

// guardedTx is the Tx of a GuardedClient.
type guardedTx struct {
	inner Tx
	owner *GuardedClient
}

func (t *guardedTx) Query(ctx context.Context, dest any, query string, args ...any) error {
	if err := t.owner.check(query); err != nil {
		return err
	}
	return t.inner.Query(ctx, dest, query, args...)
}

func (t *guardedTx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := t.owner.check(query); err != nil {
		return nil, err
	}
	return t.inner.Exec(ctx, query, args...)
}

func (t *guardedTx) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(nil, table)
}

func (t *guardedTx) Save(context.Context, any) error { return t.owner.refuse("Save") }

func (t *guardedTx) Remove(context.Context, any) error { return t.owner.refuse("Remove") }
