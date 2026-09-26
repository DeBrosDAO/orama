package triggers

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func migratedCronStore(t *testing.T) *CronTriggerStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewCronTriggerStore(rqlite.NewClient(db), zap.NewNop())
}

func (s *CronScheduler) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}

// The gateway starts the scheduler once its schema is up, from a goroutine of
// its own, while Close may stop it at any moment. A Start that loses that race
// must not leave a loop running after Stop.
func TestCronScheduler_StartAfterStopIsANoOp(t *testing.T) {
	s := NewCronScheduler(migratedCronStore(t), nil, zap.NewNop(), time.Hour)
	s.Stop()
	s.Start(context.Background())
	if s.running() {
		s.Stop()
		t.Fatal("Start after Stop started the loop")
	}
}

func TestCronScheduler_StartTwiceRunsOneLoop(t *testing.T) {
	s := NewCronScheduler(migratedCronStore(t), nil, zap.NewNop(), time.Hour)
	s.Start(context.Background())
	s.Start(context.Background())
	if !s.running() {
		t.Fatal("not running after Start")
	}
	s.Stop()
	if s.running() {
		t.Fatal("still running after Stop")
	}
}

// Run with -race: Start and Stop from different goroutines.
func TestCronScheduler_ConcurrentStartStop(t *testing.T) {
	s := NewCronScheduler(migratedCronStore(t), nil, zap.NewNop(), time.Hour)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.Start(context.Background()) }()
	go func() { defer wg.Done(); s.Stop() }()
	wg.Wait()
	s.Stop()
	if s.running() {
		t.Fatal("running after the final Stop")
	}
}
