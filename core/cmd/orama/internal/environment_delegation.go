package cli

import (
	"fmt"
	"time"
)

// DelegationStatus is the last DNS check of one cluster domain: whether the
// parent zone returned the NS and glue records the cluster expects, and what
// it got wrong when it did not.
type DelegationStatus struct {
	Domain    string   `json:"domain"`
	Delegated bool     `json:"delegated"`
	CheckedAt string   `json:"checked_at"`
	Findings  []string `json:"findings,omitempty"`
}

// RecordDelegation stores the result of a delegation check on the
// environment, replacing the earlier result for the same domain.
func RecordDelegation(envName string, status DelegationStatus) error {
	if status.Domain == "" {
		return fmt.Errorf("a delegation result needs a domain")
	}
	if status.CheckedAt == "" {
		status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return updateEnvironmentConfig(func(cfg *EnvironmentConfig) error {
		for i := range cfg.Environments {
			if cfg.Environments[i].Name != envName {
				continue
			}
			list := cfg.Environments[i].Delegations
			for j := range list {
				if list[j].Domain == status.Domain {
					list[j] = status
					return nil
				}
			}
			cfg.Environments[i].Delegations = append(list, status)
			return nil
		}
		return fmt.Errorf("environment %q is not configured; add it with `orama env add` before recording its delegation", envName)
	})
}
