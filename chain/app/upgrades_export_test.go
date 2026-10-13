package app

import "github.com/DeBrosOfficial/network/chain/app/upgrades"

// SetUpgradesForTest wires list as the registered upgrades of this app, as NewOramaApp wires
// upgrades.Registered, without reading any upgrade info from disk.
func (app *OramaApp) SetUpgradesForTest(list []upgrades.Upgrade) { app.setUpgrades(list, "") }

// UpgradeStoreLoaderForTest exposes upgradeStoreLoader.
var UpgradeStoreLoaderForTest = upgradeStoreLoader
