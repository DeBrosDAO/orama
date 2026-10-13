package autoupdate

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

// SettingsFrom is the policy the cluster has stored (rows of cluster_settings
// by key; a key that is absent is its default) for a node of the given role.
// A stored value the policy refuses is an error and not the default: the
// operator believes it is in force.
func SettingsFrom(stored map[string]string, role string) (Settings, error) {
	value := func(key, fallback string) (string, error) {
		v, ok := stored[key]
		if !ok {
			return fallback, nil
		}
		if err := updatepolicy.Validate(key, v); err != nil {
			return "", fmt.Errorf("cluster setting %s is %q: %w", key, v, err)
		}
		return v, nil
	}
	mode, err := value(updatepolicy.KeyMode, updatepolicy.DefaultMode)
	if err != nil {
		return Settings{}, err
	}
	channel, err := value(updatepolicy.KeyChannel, updatepolicy.DefaultChannel)
	if err != nil {
		return Settings{}, err
	}
	windowText, err := value(updatepolicy.KeyWindow, updatepolicy.DefaultWindow)
	if err != nil {
		return Settings{}, err
	}
	repo, err := value(updatepolicy.KeyRepo, updatepolicy.DefaultRepo)
	if err != nil {
		return Settings{}, err
	}
	window, err := updatepolicy.ParseWindow(windowText)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Mode: mode, Channel: channel, MaxParallel: 1, Role: role,
		WindowStart: window.Start, WindowEnd: window.End, RepoURL: repo,
	}, nil
}
