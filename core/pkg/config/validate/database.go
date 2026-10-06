package validate

import (
	"fmt"
	"time"
)

// DatabaseConfig represents the database configuration for validation purposes.
type DatabaseConfig struct {
	DataDir             string
	ReplicationFactor   int
	ShardCount          int
	MaxDatabaseSize     int64
	RQLitePort          int
	RQLiteRaftPort      int
	RQLiteJoinAddress   string
	ClusterSyncInterval time.Duration
	PeerInactivityLimit time.Duration
	MinClusterSize      int
	RQLiteAuthFile      string
	RQLiteEnforceAuth   bool
}

// ValidateDatabase performs validation of the database configuration.
func ValidateDatabase(dc DatabaseConfig) []error {
	var errs []error

	// Enforcement with no auth file starts rqlited with `-auth ""`, which it
	// rejects — so the node fails to start with an rqlited usage error rather
	// than a config error naming the setting that is wrong.
	if dc.RQLiteEnforceAuth && dc.RQLiteAuthFile == "" {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_enforce_auth",
			Message: "is set but database.rqlite_auth_file is empty; rqlited has no credentials to enforce",
			Hint:    "set database.rqlite_auth_file to the rqlite auth JSON (secrets/rqlite-auth.json), or unset rqlite_enforce_auth",
		})
	}

	// Validate data_dir
	if dc.DataDir == "" {
		errs = append(errs, ValidationError{
			Path:    "database.data_dir",
			Message: "must not be empty",
		})
	} else {
		if err := ValidateDataDir(dc.DataDir); err != nil {
			errs = append(errs, ValidationError{
				Path:    "database.data_dir",
				Message: err.Error(),
			})
		}
	}

	// Validate replication_factor
	if dc.ReplicationFactor < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.replication_factor",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.ReplicationFactor),
		})
	} else if dc.ReplicationFactor%2 == 0 {
		errs = append(errs, ValidationError{
			Path:    "database.replication_factor",
			Message: fmt.Sprintf("value %d is even; Raft recommends odd numbers for quorum", dc.ReplicationFactor),
			Hint:    "use 1, 3, or 5 for proper Raft consensus",
		})
	}

	// Validate shard_count
	if dc.ShardCount < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.shard_count",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.ShardCount),
		})
	}

	// Validate max_database_size
	if dc.MaxDatabaseSize < 0 {
		errs = append(errs, ValidationError{
			Path:    "database.max_database_size",
			Message: fmt.Sprintf("must be >= 0; got %d", dc.MaxDatabaseSize),
		})
	}

	// Validate rqlite_port
	if dc.RQLitePort < 1 || dc.RQLitePort > 65535 {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_port",
			Message: fmt.Sprintf("must be between 1 and 65535; got %d", dc.RQLitePort),
		})
	}

	// Validate rqlite_raft_port
	if dc.RQLiteRaftPort < 1 || dc.RQLiteRaftPort > 65535 {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_raft_port",
			Message: fmt.Sprintf("must be between 1 and 65535; got %d", dc.RQLiteRaftPort),
		})
	}

	// Ports must differ
	if dc.RQLitePort == dc.RQLiteRaftPort {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_raft_port",
			Message: fmt.Sprintf("must differ from database.rqlite_port (%d)", dc.RQLitePort),
		})
	}

	// Validate rqlite_join_address format if provided (optional for all nodes)
	// The first node in a cluster won't have a join address; subsequent nodes will
	if dc.RQLiteJoinAddress != "" {
		if err := ValidateHostPort(dc.RQLiteJoinAddress); err != nil {
			errs = append(errs, ValidationError{
				Path:    "database.rqlite_join_address",
				Message: err.Error(),
				Hint:    "expected format: host:port",
			})
		}
	}

	// Validate cluster_sync_interval
	if dc.ClusterSyncInterval != 0 && dc.ClusterSyncInterval < 10*time.Second {
		errs = append(errs, ValidationError{
			Path:    "database.cluster_sync_interval",
			Message: fmt.Sprintf("must be >= 10s or 0 (for default); got %v", dc.ClusterSyncInterval),
			Hint:    "recommended: 30s",
		})
	}

	// Validate peer_inactivity_limit
	if dc.PeerInactivityLimit != 0 {
		if dc.PeerInactivityLimit < time.Hour {
			errs = append(errs, ValidationError{
				Path:    "database.peer_inactivity_limit",
				Message: fmt.Sprintf("must be >= 1h or 0 (for default); got %v", dc.PeerInactivityLimit),
				Hint:    "recommended: 24h",
			})
		} else if dc.PeerInactivityLimit > 7*24*time.Hour {
			errs = append(errs, ValidationError{
				Path:    "database.peer_inactivity_limit",
				Message: fmt.Sprintf("must be <= 7d; got %v", dc.PeerInactivityLimit),
				Hint:    "recommended: 24h",
			})
		}
	}

	// Validate min_cluster_size
	if dc.MinClusterSize < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.min_cluster_size",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.MinClusterSize),
		})
	}

	return errs
}
