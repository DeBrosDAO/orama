package app

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/baseapp"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/DeBrosOfficial/network/chain/app/upgrades"
)

// setUpgrades wires every registered governed upgrade (app/upgrades): its handler under the plan
// name for x/upgrade to run at the plan's height, and its store upgrades into the store loader
// when the node starts at that height. Release builds register what upgrades.Registered lists;
// with none registered it touches nothing, not even the disk.
func (app *OramaApp) setUpgrades(list []upgrades.Upgrade, homePath string) {
	if err := upgrades.Validate(list); err != nil {
		panic(err)
	}
	deps := upgrades.Deps{ModuleManager: app.ModuleManager, Configurator: app.configurator}
	for _, u := range list {
		app.UpgradeKeeper.SetUpgradeHandler(u.Name, u.CreateHandler(deps))
	}
	if homePath == "" || !anyStoreUpgrades(list) {
		return
	}
	info, err := app.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		panic(fmt.Errorf("failed to read the upgrade info under %s: %w", homePath, err))
	}
	if loader := upgradeStoreLoader(list, info, app.UpgradeKeeper.IsSkipHeight(info.Height)); loader != nil {
		app.SetStoreLoader(loader)
	}
}

func anyStoreUpgrades(list []upgrades.Upgrade) bool {
	for _, u := range list {
		if u.StoreUpgrades != nil {
			return true
		}
	}
	return false
}

// upgradeStoreLoader is the store loader for the upgrade info cosmovisor left on disk (the plan
// the old binary halted for), or nil when that plan is none, is skipped by operator flag, or
// changes no stores.
func upgradeStoreLoader(list []upgrades.Upgrade, info upgradetypes.Plan, skipped bool) baseapp.StoreLoader {
	if info.Name == "" || skipped {
		return nil
	}
	storeUpgrades := upgrades.StoreUpgradesFor(list, info.Name)
	if storeUpgrades == nil {
		return nil
	}
	return upgradetypes.UpgradeStoreLoader(info.Height, storeUpgrades)
}
