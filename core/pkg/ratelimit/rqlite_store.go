package ratelimit

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// rqliteStore is the production ConfigStore — persists per-namespace
// overrides in the `namespace_rate_limit_config` table (migration 027).
type rqliteStore struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewRqliteConfigStore returns a ConfigStore backed by RQLite.
func NewRqliteConfigStore(db rqlite.Client, logger *zap.Logger) ConfigStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &rqliteStore{db: db, logger: logger}
}

func (s *rqliteStore) Get(ctx context.Context, namespace string) (*Config, error) {
	var rows []struct {
		Namespace         string `db:"namespace"`
		RequestsPerMinute int    `db:"requests_per_minute"`
		Burst             int    `db:"burst"`
		UpdatedAt         int64  `db:"updated_at"`
		UpdatedBy         string `db:"updated_by"`
	}
	err := s.db.Query(ctx, &rows,
		`SELECT namespace, requests_per_minute, burst, updated_at, updated_by
		 FROM namespace_rate_limit_config WHERE namespace = ? LIMIT 1`, namespace)
	if err != nil {
		return nil, fmt.Errorf("rate-limit config Get: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	return &Config{
		Namespace:         r.Namespace,
		RequestsPerMinute: r.RequestsPerMinute,
		Burst:             r.Burst,
		UpdatedAt:         r.UpdatedAt,
		UpdatedBy:         r.UpdatedBy,
	}, nil
}

func (s *rqliteStore) Upsert(ctx context.Context, cfg Config) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("namespace required")
	}
	if cfg.RequestsPerMinute <= 0 || cfg.Burst <= 0 {
		return fmt.Errorf("requests_per_minute and burst must be > 0")
	}
	// SQLite UPSERT — single Raft commit, no read-then-write race.
	_, err := s.db.Exec(ctx,
		`INSERT INTO namespace_rate_limit_config
		   (namespace, requests_per_minute, burst, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(namespace) DO UPDATE SET
		   requests_per_minute = excluded.requests_per_minute,
		   burst               = excluded.burst,
		   updated_at          = excluded.updated_at,
		   updated_by          = excluded.updated_by`,
		cfg.Namespace, cfg.RequestsPerMinute, cfg.Burst, cfg.UpdatedAt, cfg.UpdatedBy)
	if err != nil {
		return fmt.Errorf("rate-limit config Upsert: %w", err)
	}
	return nil
}

func (s *rqliteStore) Delete(ctx context.Context, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace required")
	}
	_, err := s.db.Exec(ctx,
		`DELETE FROM namespace_rate_limit_config WHERE namespace = ?`, namespace)
	if err != nil {
		return fmt.Errorf("rate-limit config Delete: %w", err)
	}
	return nil
}
