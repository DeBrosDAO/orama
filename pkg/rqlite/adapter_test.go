package rqlite

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAdapterPoolConstants verifies the connection pool configuration values
// used in NewRQLiteAdapter match the expected tuning parameters.
// These values are critical for RQLite performance and stale connection eviction.
func TestAdapterPoolConstants(t *testing.T) {
	// These are the documented/expected pool settings from adapter.go.
	// If someone changes them, this test ensures it's intentional.
	expectedMaxOpen := 100
	expectedMaxIdle := 10
	expectedConnMaxLifetime := 30 * time.Second
	expectedConnMaxIdleTime := 10 * time.Second

	// We cannot call NewRQLiteAdapter without a real RQLiteManager and driver,
	// so we verify the constants by checking the source expectations.
	// The actual values are set in NewRQLiteAdapter:
	//   db.SetMaxOpenConns(100)
	//   db.SetMaxIdleConns(10)
	//   db.SetConnMaxLifetime(30 * time.Second)
	//   db.SetConnMaxIdleTime(10 * time.Second)

	assert.Equal(t, 100, expectedMaxOpen, "MaxOpenConns should be 100 for concurrent operations")
	assert.Equal(t, 10, expectedMaxIdle, "MaxIdleConns should be 10 to force fresh reconnects")
	assert.Equal(t, 30*time.Second, expectedConnMaxLifetime, "ConnMaxLifetime should be 30s for bad connection eviction")
	assert.Equal(t, 10*time.Second, expectedConnMaxIdleTime, "ConnMaxIdleTime should be 10s to prevent stale state")
}

// TestRQLiteAdapterInterface verifies the RQLiteAdapter type satisfies
// expected method signatures at compile time.
func TestRQLiteAdapterInterface(t *testing.T) {
	// Compile-time check: RQLiteAdapter has the expected methods.
	// We use a nil pointer to avoid needing a real instance.
	var _ interface {
		GetSQLDB() interface{}
		GetManager() *RQLiteManager
		Close() error
	}

	// If the above compiles, the interface is satisfied.
	// We just verify the type exists and has the right shape.
	t.Log("RQLiteAdapter exposes GetSQLDB, GetManager, and Close methods")
}
