package autoupdate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

const (
	// LockName is the cluster_locks row a rollout takes.
	LockName = "autoupdate"
	// LockTTL is how long a node may hold the rollout lock: longer than the
	// longest an install can take (installBudget), so a node that dies in the
	// middle frees the lock for the next by the lease running out.
	LockTTL = 45 * time.Minute
	// liveWithin is how recently a member must have heartbeated (every 30s)
	// to count as live.
	liveWithin = 5 * time.Minute
)

// Opener returns a handle on the index RQLite and the function that closes it.
//
// Each Store call opens its own. An install restarts this node's own RQLite,
// and a handle held across that has no reason to work after it; the record of
// the result and the freeing of the lock are the two calls that must.
type Opener func(ctx context.Context) (*sql.DB, func(), error)

// SQLStore is the Store over the index RQLite, through database/sql.
type SQLStore struct {
	Open Opener
}

var _ Store = SQLStore{}

// Stored reads the auto-update rows of cluster_settings.
func (s SQLStore) Stored(ctx context.Context) (map[string]string, error) {
	db, closeDB, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	rows, err := rqlite.SafeQueryContext(db, ctx,
		`SELECT key, value FROM cluster_settings WHERE key IN (?, ?, ?, ?)`,
		updatepolicy.KeyMode, updatepolicy.KeyChannel, updatepolicy.KeyWindow, updatepolicy.KeyRepo)
	if err != nil {
		return nil, fmt.Errorf("read the cluster's update settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("read a cluster setting: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

// Members reads the registered, not retired, nodes. A node is live when its
// status is active and it heartbeated within liveWithin.
func (s SQLStore) Members(ctx context.Context) ([]Member, error) {
	db, closeDB, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	rows, err := rqlite.SafeQueryContext(db, ctx,
		`SELECT id, COALESCE(internal_ip, ''), COALESCE(role, 'node'), status,
		        CASE WHEN last_seen >= datetime('now', ?) THEN 1 ELSE 0 END
		   FROM dns_nodes WHERE last_seen <> ? ORDER BY id`,
		fmt.Sprintf("-%d seconds", int(liveWithin.Seconds())), constants.RetiredNodeLastSeen)
	if err != nil {
		return nil, fmt.Errorf("read the cluster's nodes: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var status string
		var fresh int
		if err := rows.Scan(&m.ID, &m.InternalIP, &m.Role, &status, &fresh); err != nil {
			return nil, fmt.Errorf("read a node of the cluster: %w", err)
		}
		m.Live = status == "active" && fresh == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// Installs reads what each node recorded for version.
func (s SQLStore) Installs(ctx context.Context, version string) (map[string]string, error) {
	db, closeDB, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	rows, err := rqlite.SafeQueryContext(db, ctx,
		`SELECT node_id, state FROM release_installs WHERE version = ?`, version)
	if err != nil {
		return nil, fmt.Errorf("read the installs of release %s: %w", version, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var node, state string
		if err := rows.Scan(&node, &state); err != nil {
			return nil, fmt.Errorf("read an install of release %s: %w", version, err)
		}
		out[node] = state
	}
	return out, rows.Err()
}

// Record writes a node's state for version, replacing an earlier one.
func (s SQLStore) Record(ctx context.Context, version, nodeID, state, detail string) error {
	switch state {
	case StateInstalled, StateFailed:
	default:
		return fmt.Errorf("install state %q is not %s or %s", state, StateInstalled, StateFailed)
	}
	db, closeDB, err := s.Open(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	_, err = rqlite.SafeExecContext(db, ctx,
		`INSERT INTO release_installs (version, node_id, state, detail) VALUES (?, ?, ?, ?)
		 ON CONFLICT(version, node_id) DO UPDATE SET
		   state = excluded.state, detail = excluded.detail, recorded_at = CURRENT_TIMESTAMP`,
		version, nodeID, state, detail)
	if err != nil {
		return fmt.Errorf("record that node %s %s release %s: %w", nodeID, state, version, err)
	}
	return nil
}

// Lock takes the rollout lock, refusing at once when another node holds it.
//
// A lease this node took earlier and never freed (a run that was killed) is
// taken back in one statement: the holder is the node's id, and a node runs one
// agent at a time (the run lock on the machine), so a lease in its own name is
// its own run's. The release function opens a handle of its own.
func (s SQLStore) Lock(ctx context.Context, holder string) (func(context.Context) error, error) {
	db, closeDB, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	if _, err := rqlite.AcquireOwnClusterLock(ctx, db, LockName, holder, LockTTL); err != nil {
		return nil, fmt.Errorf("take the rollout lock: %w", err)
	}
	return func(ctx context.Context) error {
		db, closeDB, err := s.Open(ctx)
		if err != nil {
			return err
		}
		defer closeDB()
		return rqlite.ReleaseClusterLock(ctx, db, LockName, holder)
	}, nil
}
