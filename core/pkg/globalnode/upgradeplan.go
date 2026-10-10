package globalnode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// currentPlanPath is x/upgrade's query for the plan the chain has scheduled.
const currentPlanPath = "/cosmos/upgrade/v1beta1/current_plan"

// planNamePattern is an upgrade name the cosmovisor layout accepts.
var planNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// CurrentUpgradePlan is the name of the software upgrade the chain has
// scheduled, read from the chain's REST API at restBase, or "" when none is
// scheduled. A plan only exists once a governance proposal for it has passed,
// so it is the one thing that makes staging a new chain binary right.
func CurrentUpgradePlan(ctx context.Context, restBase string, client *http.Client) (string, error) {
	raw, err := (&chainread.Reader{REST: restBase, HTTP: client}).RESTGet(ctx, currentPlanPath)
	if err != nil {
		return "", fmt.Errorf("read the chain's scheduled upgrade: %w", err)
	}
	var resp struct {
		Plan *struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("the chain answered a malformed upgrade plan: %w", err)
	}
	if resp.Plan == nil || resp.Plan.Name == "" {
		return "", nil
	}
	if !planNamePattern.MatchString(resp.Plan.Name) {
		return "", fmt.Errorf("the chain's scheduled upgrade is named %q, which cosmovisor cannot stage (use lowercase letters, digits, '.', '_' and '-')", resp.Plan.Name)
	}
	return resp.Plan.Name, nil
}
