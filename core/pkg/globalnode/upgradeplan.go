package globalnode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// currentPlanPath is x/upgrade's query for the plan the chain has scheduled.
const currentPlanPath = "/cosmos/upgrade/v1beta1/current_plan"

// planNamePattern is an upgrade name the cosmovisor layout accepts.
var planNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// UpgradePlan is the software upgrade the chain has scheduled.
type UpgradePlan struct {
	// Name is the plan's name; empty means none is scheduled.
	Name string
	// Info is the plan's free-form info, which cosmovisor reads as JSON when it
	// carries the binaries' download URLs and checksums.
	Info string
}

// checksumPattern is a sha256 named in a binary's URL the way cosmovisor reads
// it: "...?checksum=sha256:<hex>".
var checksumPattern = regexp.MustCompile(`(?i)checksum=sha256:([0-9a-f]{64})`)

// BinaryChecksum is the sha256 the plan's info names for the chain binary of
// osArch ("linux/amd64"), or for "any". named is false when the info names none.
func (p UpgradePlan) BinaryChecksum(osArch string) (sum string, named bool) {
	var info struct {
		Binaries map[string]string `json:"binaries"`
	}
	if json.Unmarshal([]byte(p.Info), &info) != nil {
		return "", false
	}
	for _, key := range []string{osArch, "any"} {
		if m := checksumPattern.FindStringSubmatch(info.Binaries[key]); m != nil {
			return strings.ToLower(m[1]), true
		}
	}
	return "", false
}

// CurrentUpgradePlan is the software upgrade the chain has scheduled, read from
// the chain's REST API at restBase; its Name is empty when none is scheduled. A
// plan only exists once a governance proposal for it has passed, so it is the one
// thing that makes staging a new chain binary right.
func CurrentUpgradePlan(ctx context.Context, restBase string, client *http.Client) (UpgradePlan, error) {
	raw, err := (&chainread.Reader{REST: restBase, HTTP: client}).RESTGet(ctx, currentPlanPath)
	if err != nil {
		return UpgradePlan{}, fmt.Errorf("read the chain's scheduled upgrade: %w", err)
	}
	var resp struct {
		Plan *struct {
			Name string `json:"name"`
			Info string `json:"info"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return UpgradePlan{}, fmt.Errorf("the chain answered a malformed upgrade plan: %w", err)
	}
	if resp.Plan == nil || resp.Plan.Name == "" {
		return UpgradePlan{}, nil
	}
	if !planNamePattern.MatchString(resp.Plan.Name) {
		return UpgradePlan{}, fmt.Errorf("the chain's scheduled upgrade is named %q, which cosmovisor cannot stage (use lowercase letters, digits, '.', '_' and '-')", resp.Plan.Name)
	}
	return UpgradePlan{Name: resp.Plan.Name, Info: resp.Plan.Info}, nil
}
