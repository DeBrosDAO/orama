package rqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// scopeProbe is the Client a GuardedClient wraps: it records what reached
// it.
type scopeProbe struct {
	Client
	queries []string
	execs   []string
	batched []string
	txs     int
}

func (r *scopeProbe) Query(_ context.Context, _ any, query string, _ ...any) error {
	r.queries = append(r.queries, query)
	return nil
}

func (r *scopeProbe) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	r.execs = append(r.execs, query)
	return nil, nil
}

func (r *scopeProbe) Batch(_ context.Context, ops []BatchOp) (*BatchResult, error) {
	for _, op := range ops {
		r.batched = append(r.batched, op.SQL)
	}
	return &BatchResult{Committed: true}, nil
}

func (r *scopeProbe) BatchQuery(_ context.Context, ops []BatchOp) ([]OpResult, error) {
	for _, op := range ops {
		r.batched = append(r.batched, op.SQL)
	}
	return nil, nil
}

func (r *scopeProbe) Tx(_ context.Context, fn func(tx Tx) error) error {
	r.txs++
	return fn(&scopeProbeTx{owner: r})
}

type scopeProbeTx struct {
	Tx
	owner *scopeProbe
}

func (t *scopeProbeTx) Query(_ context.Context, _ any, query string, _ ...any) error {
	t.owner.queries = append(t.owner.queries, query)
	return nil
}

func (t *scopeProbeTx) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	t.owner.execs = append(t.owner.execs, query)
	return nil, nil
}

func refuseSecrets(query string) error {
	if strings.Contains(query, "secrets") {
		return errors.New("secrets are out of scope")
	}
	return nil
}

func newGuarded() (*GuardedClient, *scopeProbe) {
	inner := &scopeProbe{}
	return NewGuardedClient(inner, "test scope", refuseSecrets), inner
}

func TestGuardedClient_passesAnAllowedStatementThrough(t *testing.T) {
	c, inner := newGuarded()
	ctx := context.Background()

	if err := c.Query(ctx, nil, "SELECT * FROM deployments"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if _, err := c.Exec(ctx, "DELETE FROM deployments"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(inner.queries) != 1 || len(inner.execs) != 1 {
		t.Fatalf("queries=%v execs=%v, want one of each", inner.queries, inner.execs)
	}
}

func TestGuardedClient_refusesAStatementTheGuardRefusesAndNeverSendsIt(t *testing.T) {
	c, inner := newGuarded()
	ctx := context.Background()

	err := c.Query(ctx, nil, "SELECT * FROM secrets")
	if err == nil || !strings.Contains(err.Error(), "test scope") {
		t.Fatalf("Query err = %v, want a refusal naming the scope", err)
	}
	if _, err := c.Exec(ctx, "DELETE FROM secrets"); err == nil {
		t.Fatal("Exec reached a refused table")
	}
	if len(inner.queries)+len(inner.execs) != 0 {
		t.Fatalf("a refused statement reached the database: %v %v", inner.queries, inner.execs)
	}
}

func TestGuardedClient_guardsStatementsInsideATransaction(t *testing.T) {
	c, inner := newGuarded()
	err := c.Tx(context.Background(), func(tx Tx) error {
		if _, err := tx.Exec(context.Background(), "UPDATE deployments SET status = 'x'"); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), "UPDATE secrets SET v = 1")
		return err
	})
	if err == nil {
		t.Fatal("a refused statement ran inside a transaction")
	}
	if len(inner.execs) != 1 || strings.Contains(inner.execs[0], "secrets") {
		t.Fatalf("execs = %v, want only the allowed one", inner.execs)
	}
}

func TestGuardedClient_oneRefusedOpRefusesTheWholeBatch(t *testing.T) {
	c, inner := newGuarded()
	ops := []BatchOp{
		{Kind: BatchOpExec, SQL: "UPDATE deployments SET status = 'x'"},
		{Kind: BatchOpExec, SQL: "UPDATE secrets SET v = 1"},
	}
	if _, err := c.Batch(context.Background(), ops); err == nil {
		t.Fatal("a batch with a refused op ran")
	}
	if _, err := c.BatchQuery(context.Background(), ops); err == nil {
		t.Fatal("a query batch with a refused op ran")
	}
	if len(inner.batched) != 0 {
		t.Fatalf("part of a refused batch was sent: %v", inner.batched)
	}
	if _, err := c.Batch(context.Background(), nil); err != nil {
		t.Fatalf("an empty batch was refused: %v", err)
	}
}

func TestGuardedClient_theEntityLayerIsOutsideTheScope(t *testing.T) {
	c, _ := newGuarded()
	ctx := context.Background()
	for name, err := range map[string]error{
		"FindBy":    c.FindBy(ctx, nil, "deployments", nil),
		"FindOneBy": c.FindOneBy(ctx, nil, "deployments", nil),
		"Save":      c.Save(ctx, struct{}{}),
		"Remove":    c.Remove(ctx, struct{}{}),
	} {
		if !errors.Is(err, ErrOutsideScope) {
			t.Errorf("%s err = %v, want ErrOutsideScope", name, err)
		}
	}
	if _, _, err := c.BatchWithSeq(ctx, "ns", nil); !errors.Is(err, ErrOutsideScope) {
		t.Errorf("BatchWithSeq err = %v, want ErrOutsideScope", err)
	}
	if c.Repository("deployments") != nil {
		t.Error("Repository handed out a repository that bypasses the guard")
	}
}
