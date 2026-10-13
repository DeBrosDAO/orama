package install

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

const (
	// updatePolicyWriter is the updated_by of a setting the install wrote.
	updatePolicyWriter = "install:genesis"
	// updatePolicyBudget bounds the writes of a genesis install's update settings.
	updatePolicyBudget = 30 * time.Second
	// readStrong reads through the leader, so a row an operator wrote is seen.
	readStrong rqlite.ReadConsistency = "strong"
	// seedSettingSQL stores a setting only when the cluster has none: a value an
	// operator already set (a re-run of the install over a cluster that exists)
	// is the one in force.
	seedSettingSQL = `INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO NOTHING`
)

// settingExec runs one write against the cluster's settings table.
type settingExec func(ctx context.Context, query string, args ...any) error

// SeedUpdatePolicy stores the release repository and channel of the network a
// genesis install creates as the cluster's update settings (release_repo and
// update_channel), so the auto-update agent has a repository to look in and the
// channel the network's nodes run. It must run once the node is up: the table
// is created by the node's migrations. An empty value is not stored (the
// default stays), and neither is a setting that already has a row. auto_update
// is not touched: the cluster keeps the default, notify.
func (ps *ProductionSetup) SeedUpdatePolicy(ctx context.Context, repo, channel string) error {
	if repo == "" && channel == "" {
		return nil
	}
	ep, err := rqlite.EndpointFromNodeConfig(OramaRoot(ps.oramaDir), ps.nodeConfigPath())
	if err != nil {
		return fmt.Errorf("find this node's rqlite to store the update settings: %w", err)
	}
	db, err := sql.Open("rqlite", ep.SQLDSN(readStrong))
	if err != nil {
		return fmt.Errorf("open the rqlite at %s to store the update settings: %w", ep.BaseURL(), err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, updatePolicyBudget)
	defer cancel()
	exec := func(ctx context.Context, query string, args ...any) error {
		_, err := rqlite.SafeExecContext(db, ctx, query, args...)
		return err
	}
	return seedUpdatePolicy(ctx, exec, repo, channel)
}

// seedUpdatePolicy validates the non-empty values, then stores them.
func seedUpdatePolicy(ctx context.Context, exec settingExec, repo, channel string) error {
	settings := []struct{ key, value string }{
		{updatepolicy.KeyRepo, repo},
		{updatepolicy.KeyChannel, channel},
	}
	for _, s := range settings {
		if s.value == "" {
			continue
		}
		if err := updatepolicy.Validate(s.key, s.value); err != nil {
			return fmt.Errorf("the update setting %s: %w", s.key, err)
		}
	}
	for _, s := range settings {
		if s.value == "" {
			continue
		}
		if err := exec(ctx, seedSettingSQL, s.key, s.value, updatePolicyWriter); err != nil {
			return fmt.Errorf("store the update setting %s in the cluster's settings: %w", s.key, err)
		}
	}
	return nil
}
