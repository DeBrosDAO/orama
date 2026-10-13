package validate

import (
	"strings"
	"testing"
)

func baseDB() DatabaseConfig {
	return DatabaseConfig{
		DataDir:           "/tmp/orama-data",
		ReplicationFactor: 3,
		ShardCount:        16,
		MaxDatabaseSize:   1 << 30,
		RQLitePort:        10100,
		RQLiteRaftPort:    10101,
		MinClusterSize:    1,
	}
}

// Enforcement with nothing to enforce must be a config error, named after the
// setting that is wrong — not an rqlited usage error at start-up.
func TestValidateDatabase_enforce_auth_without_file(t *testing.T) {
	dc := baseDB()
	dc.RQLiteEnforceAuth = true

	errs := ValidateDatabase(dc)
	var found bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "rqlite_enforce_auth") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error naming rqlite_enforce_auth; got %v", errs)
	}
}

func TestValidateDatabase_enforce_auth_with_file(t *testing.T) {
	dc := baseDB()
	dc.RQLiteEnforceAuth = true
	dc.RQLiteAuthFile = "/home/orama/.orama/secrets/rqlite-auth.json"

	for _, err := range ValidateDatabase(dc) {
		if strings.Contains(err.Error(), "rqlite_enforce_auth") {
			t.Fatalf("rejected a complete auth configuration: %v", err)
		}
	}
}

// Credentials without enforcement is the normal state during the first pass of
// the rollout and must validate cleanly.
func TestValidateDatabase_auth_file_without_enforce(t *testing.T) {
	dc := baseDB()
	dc.RQLiteAuthFile = "/home/orama/.orama/secrets/rqlite-auth.json"

	for _, err := range ValidateDatabase(dc) {
		if strings.Contains(err.Error(), "rqlite_") && strings.Contains(err.Error(), "auth") {
			t.Fatalf("rejected credentials-without-enforcement: %v", err)
		}
	}
}
