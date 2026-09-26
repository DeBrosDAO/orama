package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// postSchemaGateway is a gateway whose auth service signs with its own key and
// whose registry is db — with or without the schema, as the test decides.
func postSchemaGateway(t *testing.T, db *sql.DB) (*Gateway, *Dependencies) {
	t.Helper()
	svc, err := gwauth.NewService(newRQLiteTestLogger(), &sqliteKeyNet{db: &sqliteKeyQuerier{db: db}}, "", "anchat")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEdDSAKey(priv, "anchat")
	return &Gateway{logger: newRQLiteTestLogger()}, &Dependencies{AuthService: svc}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1) // one in-memory database, not one per connection
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrate(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

// On a fresh cluster the signing_keys table does not exist when the gateway is
// built. Publishing then was a warning, and every other gateway refused this
// one's tokens until it restarted. Now the gateway stays not ready until the
// key is published.
func TestPostSchemaSteps_publishesTheSigningKeyOnceTheSchemaExists(t *testing.T) {
	db := openSQLite(t)
	g, deps := postSchemaGateway(t, db)
	steps := g.postSchemaSteps(&Config{}, deps)

	err := runPostSchemaSteps(context.Background(), steps)
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Fatalf("before the schema: got %v, want the signing-key publish to fail readiness", err)
	}

	migrate(t, db)
	if err := runPostSchemaSteps(context.Background(), steps); err != nil {
		t.Fatalf("after the schema: %v", err)
	}
	var kid string
	if err := db.QueryRow(`SELECT kid FROM signing_keys WHERE namespace = 'anchat'`).Scan(&kid); err != nil {
		t.Fatalf("the key was not published: %v", err)
	}
	if kid != deps.AuthService.SigningKID() {
		t.Errorf("published kid %q, want %q", kid, deps.AuthService.SigningKID())
	}
}

func stepNames(steps []postSchemaStep) []string {
	var names []string
	for _, s := range steps {
		names = append(names, s.name)
	}
	return names
}

// The signing key comes first and gates readiness. Housekeeping whose failure
// must not take every route down — a core-registry write from a namespace
// gateway, a backfill over many rows, the background services — runs after.
func TestPostSchemaSteps_gatingAndAfterReadyGroups(t *testing.T) {
	db := openSQLite(t)
	migrate(t, db)
	g, deps := postSchemaGateway(t, db)
	store := triggers.NewPubSubTriggerStore(rqlite.NewClient(db), zap.NewNop())
	g.pubsubDispatcher = triggers.NewPubSubDispatcher(store, nil, nil, nil, zap.NewNop())
	g.cronScheduler = triggers.NewCronScheduler(triggers.NewCronTriggerStore(rqlite.NewClient(db), zap.NewNop()), nil, zap.NewNop(), 0)
	t.Cleanup(g.cronScheduler.Stop)
	deps.PushDeviceStore = &fakeBackfiller{}

	gating := stepNames(g.postSchemaSteps(&Config{APIKeyHMACSecret: "s"}, deps))
	if want := []string{"publish this gateway's signing key", "hash plaintext API keys"}; strings.Join(gating, "|") != strings.Join(want, "|") {
		t.Fatalf("gating steps = %q, want %q", gating, want)
	}
	after := g.afterReadySteps(context.Background(), deps)
	want := []string{
		"revoke API keys of deleted namespaces",
		"backfill push token fingerprints",
		"start the pubsub trigger dispatcher",
		"start the cron scheduler",
	}
	if got := stepNames(after); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("after-ready steps = %q\nwant %q", got, want)
	}
	if err := runPostSchemaSteps(context.Background(), after); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// fakeBackfiller is a push device store with only the backfill.
type fakeBackfiller struct {
	push.PushDeviceStore
	calls int
	errs  []error // returned in order; nil once exhausted
}

func (f *fakeBackfiller) BackfillTokenFP(context.Context) (int, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return 0, f.errs[f.calls-1]
	}
	return 0, nil
}

// The backfill used to be a background goroutine that retried "no such column"
// for five minutes and then gave up for good. It runs once the gateway is
// ready now, and is retried until it succeeds rather than logged and dropped.
func TestRunAfterReady_retriesUntilThePassSucceeds(t *testing.T) {
	shortBackoff(t)
	db := openSQLite(t)
	migrate(t, db)
	g, deps := postSchemaGateway(t, db)
	g.ready = newReadiness()
	g.ready.set(ReadinessReady, "", "")
	backfill := &fakeBackfiller{errs: []error{errors.New("leader not found"), errors.New("leader not found")}}
	deps.PushDeviceStore = backfill

	done := make(chan struct{})
	go func() {
		g.runAfterReady(context.Background(), g.afterReadySteps(context.Background(), deps))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runAfterReady did not finish after the backfill recovered")
	}
	if backfill.calls != 3 {
		t.Errorf("backfill ran %d times, want 3 (two failures, one success)", backfill.calls)
	}
}

// Nothing in the after-ready group runs against a schema that is not there.
func TestRunAfterReady_waitsForReadiness(t *testing.T) {
	g, deps := postSchemaGateway(t, openSQLite(t))
	g.ready = newReadiness()
	backfill := &fakeBackfiller{}
	deps.PushDeviceStore = backfill

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	g.runAfterReady(ctx, g.afterReadySteps(ctx, deps))
	if backfill.calls != 0 {
		t.Fatalf("backfill ran %d times before the gateway was ready", backfill.calls)
	}
}

func shortBackoff(t *testing.T) {
	t.Helper()
	prevBase, prevMax := schemaRetryBaseBackoff, schemaRetryMaxBackoff
	schemaRetryBaseBackoff, schemaRetryMaxBackoff = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { schemaRetryBaseBackoff, schemaRetryMaxBackoff = prevBase, prevMax })
}

// A gateway whose push subsystem did not come up has no backfill step.
func TestAfterReadySteps_noPushStoreNoBackfill(t *testing.T) {
	g, deps := postSchemaGateway(t, openSQLite(t))
	for _, s := range g.afterReadySteps(context.Background(), deps) {
		if strings.Contains(s.name, "push") {
			t.Fatalf("unexpected step %q without a push store", s.name)
		}
	}
}

func TestRunPostSchemaSteps_stopsAtTheFirstFailureAndNamesIt(t *testing.T) {
	var ran []string
	step := func(name string, err error) postSchemaStep {
		return postSchemaStep{name: name, run: func(context.Context) error {
			ran = append(ran, name)
			return err
		}}
	}
	err := runPostSchemaSteps(context.Background(), []postSchemaStep{
		step("one", nil), step("two", errors.New("no such table")), step("three", nil),
	})
	if err == nil || !strings.HasPrefix(err.Error(), "two: ") {
		t.Fatalf("got %v, want the failing step named", err)
	}
	if strings.Join(ran, ",") != "one,two" {
		t.Errorf("ran %v, want the steps after the failure skipped", ran)
	}
	if err := runPostSchemaSteps(context.Background(), nil); err != nil {
		t.Errorf("no steps: %v", err)
	}
}

// A start step spawns a goroutine; the readiness loop repeats every step after
// a failure, so one that already succeeded must not run again.
func TestOnceSucceeded_retriesUntilItSucceedsThenNever(t *testing.T) {
	calls := 0
	fail := true
	start := onceSucceeded(func(context.Context) error {
		calls++
		if fail {
			return errors.New("not yet")
		}
		return nil
	})

	if err := start(context.Background()); err == nil {
		t.Fatal("the first failure must be returned")
	}
	fail = false
	for i := 0; i < 3; i++ {
		if err := start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Errorf("start ran %d times, want 2 (one failure, one success)", calls)
	}
}

// A housekeeping step that keeps failing must not keep the others — the
// dispatcher and scheduler starts among them — from running.
func TestRunAfterReady_aStuckStepDoesNotBlockTheOthers(t *testing.T) {
	shortBackoff(t)
	g := &Gateway{logger: newRQLiteTestLogger(), ready: newReadiness()}
	g.ready.set(ReadinessReady, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ran := make(chan struct{})
	steps := []postSchemaStep{
		{name: "stuck", run: func(context.Context) error { return errors.New("core registry has no leader") }},
		{name: "start", run: func(context.Context) error { close(ran); return nil }},
	}
	go g.runAfterReady(ctx, steps)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("a failing step kept the next one from running")
	}
}
