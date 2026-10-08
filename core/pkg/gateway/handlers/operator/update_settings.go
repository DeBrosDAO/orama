package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

// updateSettingNames maps the CLI's hyphenated names onto the stored
// auto-update keys. The node-side agent reads the same keys
// (pkg/autoupdate), validated by the same package.
var updateSettingNames = map[string]string{
	"auto-update":    updatepolicy.KeyMode,
	"update-channel": updatepolicy.KeyChannel,
	"update-window":  updatepolicy.KeyWindow,
	"release-repo":   updatepolicy.KeyRepo,
}

// normalizeUpdateSetting returns the stored key and value for an auto-update
// setting, or ok false when key names none. A value the policy refuses is an
// error: the agent on every node reads this row, and one it cannot use would
// stop updates cluster-wide.
func normalizeUpdateSetting(key string, raw json.RawMessage) (storedKey, value string, ok bool, err error) {
	storedKey = key
	if mapped, named := updateSettingNames[key]; named {
		storedKey = mapped
	}
	if !slices.Contains(updatepolicy.Keys, storedKey) {
		return "", "", false, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", true, fmt.Errorf("%s is a string", key)
	}
	value = strings.TrimSpace(value)
	if err := updatepolicy.Validate(storedKey, value); err != nil {
		return "", "", true, err
	}
	return storedKey, value, true, nil
}

// loadUpdateSettings reads the auto-update settings with their defaults. A
// stored value the policy refuses is an error, never a default: reading it as
// the default would hide a setting an operator believes is in force.
func loadUpdateSettings(ctx context.Context, db rqlite.Client) (map[string]string, error) {
	if db == nil {
		return nil, errNoRegistry
	}
	var rows []struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}
	if err := db.Query(ctx, &rows,
		`SELECT key, value FROM cluster_settings WHERE key IN (?, ?, ?, ?)`,
		updatepolicy.KeyMode, updatepolicy.KeyChannel, updatepolicy.KeyWindow, updatepolicy.KeyRepo); err != nil {
		return nil, err
	}
	out := map[string]string{
		updatepolicy.KeyMode:    updatepolicy.DefaultMode,
		updatepolicy.KeyChannel: updatepolicy.DefaultChannel,
		updatepolicy.KeyWindow:  updatepolicy.DefaultWindow,
		updatepolicy.KeyRepo:    updatepolicy.DefaultRepo,
	}
	for _, row := range rows {
		if err := updatepolicy.Validate(row.Key, row.Value); err != nil {
			return nil, &PolicyConfigError{Reason: fmt.Sprintf("%s is %q: %v", row.Key, row.Value, err)}
		}
		out[row.Key] = row.Value
	}
	return out, nil
}
