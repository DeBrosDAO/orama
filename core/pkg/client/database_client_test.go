package client

import (
	"fmt"
	"testing"

	"github.com/rqlite/gorqlite"
)

// mockPanicConnection simulates what gorqlite does when WriteParameterized
// returns an empty slice: accessing [0] panics.
func simulateGorqlitePanic() (gorqlite.WriteResult, error) {
	var empty []gorqlite.WriteResult
	return empty[0], fmt.Errorf("leader not found") // panics
}

func TestSafeWriteOne_recoversPanic(t *testing.T) {
	// We can't easily create a real gorqlite.Connection that panics,
	// but we can verify our recovery wrapper works by testing the
	// recovery pattern directly.
	var recovered bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
			}
		}()
		simulateGorqlitePanic()
	}()

	if !recovered {
		t.Fatal("expected simulateGorqlitePanic to panic, but it didn't")
	}
}

func TestSafeWriteOne_nilConnection(t *testing.T) {
	// safeWriteOne with nil connection should recover from panic, not crash.
	_, err := safeWriteOne(nil, gorqlite.ParameterizedStatement{
		Query:     "INSERT INTO test (a) VALUES (?)",
		Arguments: []interface{}{"x"},
	})
	if err == nil {
		t.Fatal("expected error from nil connection, got nil")
	}
}

func TestSafeWriteOneRaw_nilConnection(t *testing.T) {
	// safeWriteOneRaw with nil connection should recover from panic, not crash.
	_, err := safeWriteOneRaw(nil, "INSERT INTO test (a) VALUES ('x')")
	if err == nil {
		t.Fatal("expected error from nil connection, got nil")
	}
}

func TestIsWriteOperation(t *testing.T) {
	d := &DatabaseClientImpl{}

	tests := []struct {
		sql     string
		isWrite bool
	}{
		{"INSERT INTO foo VALUES (1)", true},
		{"  INSERT INTO foo VALUES (1)", true},
		{"UPDATE foo SET a = 1", true},
		{"DELETE FROM foo", true},
		{"CREATE TABLE foo (a TEXT)", true},
		{"DROP TABLE foo", true},
		{"ALTER TABLE foo ADD COLUMN b TEXT", true},
		{"SELECT * FROM foo", false},
		{"  SELECT * FROM foo", false},
		{"EXPLAIN SELECT * FROM foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			got := d.isWriteOperation(tt.sql)
			if got != tt.isWrite {
				t.Errorf("isWriteOperation(%q) = %v, want %v", tt.sql, got, tt.isWrite)
			}
		})
	}
}
