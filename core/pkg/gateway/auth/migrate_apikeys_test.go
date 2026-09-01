package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

type memKeyDB struct {
	client.DatabaseClient
	rows    map[int64]string
	failID  int64
	updates int
}

func (m *memKeyDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(sql, "SELECT id, key"):
		var rows [][]interface{}
		for id, key := range m.rows {
			if strings.HasPrefix(key, "ak_") {
				rows = append(rows, []interface{}{id, key})
			}
		}
		return &client.QueryResult{Rows: rows, Count: int64(len(rows))}, nil
	case strings.Contains(sql, "UPDATE api_keys"):
		hashed, _ := args[0].(string)
		id := toInt64(args[1])
		old, _ := args[2].(string)
		if m.failID != 0 && id == m.failID {
			return nil, errMigrateTest
		}
		if m.rows[id] != old {
			return &client.QueryResult{Count: 0}, nil
		}
		m.rows[id] = hashed
		m.updates++
		return &client.QueryResult{Count: 1}, nil
	case strings.Contains(sql, "UPDATE namespace_ownership"):
		return &client.QueryResult{Count: 0}, nil
	default:
		return nil, errString("unexpected sql: " + sql)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

const errMigrateTest errString = "forced write error"

type memNet struct {
	client.NetworkClient
	db *memKeyDB
}

func (m *memNet) Database() client.DatabaseClient { return m.db }

func TestMigratePlaintextAPIKeys_hashesAkPrefixLeavesHex(t *testing.T) {
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	raw := "ak_legacy_one:ns"
	hexRow := HmacSHA256Hex("already-hashed-input", "test-hmac-secret")
	db := &memKeyDB{rows: map[int64]string{1: raw, 2: hexRow}}
	s.orm = &memNet{db: db}

	n, err := s.MigratePlaintextAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 1 {
		t.Fatalf("migrated %d, want 1", n)
	}
	if db.rows[1] != s.HashAPIKey(raw) {
		t.Errorf("row 1 not hashed: %q", db.rows[1])
	}
	if db.rows[2] != hexRow {
		t.Errorf("hex row must be left alone, got %q", db.rows[2])
	}
}

func TestMigratePlaintextAPIKeys_writeErrorAborts(t *testing.T) {
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	db := &memKeyDB{
		rows:   map[int64]string{1: "ak_one:ns", 2: "ak_two:ns"},
		failID: 2,
	}
	s.orm = &memNet{db: db}

	_, err := s.MigratePlaintextAPIKeys(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "forced write error") {
		t.Errorf("got %v", err)
	}
}

func TestMigratePlaintextAPIKeys_noSecretIsNoop(t *testing.T) {
	s := &Service{}
	db := &memKeyDB{rows: map[int64]string{1: "ak_one:ns"}}
	s.orm = &memNet{db: db}
	n, err := s.MigratePlaintextAPIKeys(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if db.rows[1] != "ak_one:ns" {
		t.Fatal("must not rewrite without a secret")
	}
}
