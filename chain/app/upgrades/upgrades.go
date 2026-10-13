// Package upgrades is the registry of the chain's consensus-breaking releases. A release that
// changes how the chain computes state is enacted by a governed software-upgrade plan (x/houses
// SoftwareUpgrade -> x/upgrade Plan), and the binary of that release must carry the handler
// that migrates the state at the plan's height. Each plan has one file in this package that
// exports its Upgrade, and registry.go lists it. The release declares which plan it enacts in
// the CURRENT file.
//
// See website/src/docs/operator/upgrades.mdx ("How a consensus-breaking release is made") for
// the full procedure.
package upgrades

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	housestypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// Deps are the app parts a handler migrates with. The app fills them in before it registers the
// handlers.
type Deps struct {
	ModuleManager *module.Manager
	Configurator  module.Configurator
}

// Upgrade is one governed upgrade plan the binary knows how to run.
type Upgrade struct {
	// Name is the plan's name, the one the SoftwareUpgrade proposal carries and cosmovisor stages
	// the binary under (housestypes.ValidUpgradeName).
	Name string
	// CreateHandler returns the handler x/upgrade runs at the plan's height.
	CreateHandler func(Deps) upgradetypes.UpgradeHandler
	// StoreUpgrades are the stores the plan adds, renames or deletes; nil when it changes none.
	// The app loads the stores with them when it starts at the plan's height.
	StoreUpgrades *storetypes.StoreUpgrades
}

//go:embed CURRENT
var currentFile string

// Registered returns the upgrades this binary can run, in registry order.
func Registered() []Upgrade { return append([]Upgrade(nil), registered...) }

// Lookup returns the upgrade of list named name.
func Lookup(list []Upgrade, name string) (Upgrade, bool) {
	for _, u := range list {
		if u.Name == name {
			return u, true
		}
	}
	return Upgrade{}, false
}

// Validate refuses a list that has an invalid or duplicate name or an upgrade without a handler.
// The error names every problem.
func Validate(list []Upgrade) error {
	var problems []string
	seen := map[string]bool{}
	for _, u := range list {
		switch {
		case !housestypes.ValidUpgradeName(u.Name):
			problems = append(problems, fmt.Sprintf("the name %q is not a plan name cosmovisor can stage", u.Name))
		case seen[u.Name]:
			problems = append(problems, fmt.Sprintf("the plan %q is registered twice", u.Name))
		}
		seen[u.Name] = true
		if u.CreateHandler == nil {
			problems = append(problems, fmt.Sprintf("the plan %q has no handler", u.Name))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("the upgrade registry is invalid:\n  %s", strings.Join(problems, "\n  "))
}

// Current is the plan this release declares it enacts (the CURRENT file); empty when the release
// enacts none. A plan is declared by the first line of CURRENT that is not blank or a comment.
func Current() string { return parseCurrent(currentFile) }

func parseCurrent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// CheckRelease refuses a release that declares a plan it has no handler for: its binary would
// halt the chain at the plan's height with "UPGRADE NEEDED" instead of migrating.
func CheckRelease(list []Upgrade, current string) error {
	if err := Validate(list); err != nil {
		return err
	}
	if current == "" {
		return nil
	}
	if !housestypes.ValidUpgradeName(current) {
		return fmt.Errorf("CURRENT declares %q, which is not a plan name cosmovisor can stage", current)
	}
	if _, ok := Lookup(list, current); !ok {
		return fmt.Errorf("CURRENT declares the plan %q but no handler is registered for it: add chain/app/upgrades/<plan>.go and list it in registry.go", current)
	}
	return nil
}

// StoreUpgradesFor returns the store upgrades of the plan named name, or nil when the plan is not
// in list or changes no stores.
func StoreUpgradesFor(list []Upgrade, name string) *storetypes.StoreUpgrades {
	u, ok := Lookup(list, name)
	if !ok {
		return nil
	}
	return u.StoreUpgrades
}
