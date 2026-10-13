package stategen

import (
	"fmt"
	"path/filepath"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/viper"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"

	"github.com/DeBrosOfficial/network/chain/app"
)

const (
	appConfigFile = "app.toml"
	appDBName     = "application"
	configDir     = "config"
	dataDir       = "data"
)

// OpenApp opens the chain application on the databases of a node home, built the way `oramad start`
// builds it: the options come from the home's app.toml. pruning, when it is not empty, replaces the
// pruning strategy of the file, and iavlCache, when it is above zero, its IAVL node cache size. The
// caller closes the app.
func OpenApp(home, pruning string, iavlCache int) (*app.OramaApp, error) {
	app.SetAddressPrefixes()
	v := viper.New()
	v.SetConfigFile(filepath.Join(home, configDir, appConfigFile))
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w (run `oramad-state prepare` or `oramad init` first)", filepath.Join(home, configDir, appConfigFile), err)
	}
	v.Set(flags.FlagHome, home)
	// IAVL prunes in a goroutine unless told to prune inside the commit, and that goroutine reads the
	// database the generator closes when it is done.
	v.Set(server.FlagIAVLSyncPruning, true)
	if pruning != "" {
		v.Set(server.FlagPruning, pruning)
	}
	if iavlCache > 0 {
		v.Set(server.FlagIAVLCacheSize, iavlCache)
	}
	db, err := dbm.NewDB(appDBName, server.GetAppDBBackend(v), filepath.Join(home, dataDir))
	if err != nil {
		return nil, fmt.Errorf("failed to open the application database in %s: %w", home, err)
	}
	return buildApp(db, v)
}

// buildApp constructs the application. NewOramaApp panics on a misconfiguration (a genesis without a
// chain id, a pruning strategy that does not parse); that becomes an error here.
func buildApp(db dbm.DB, v *viper.Viper) (a *app.OramaApp, err error) {
	defer func() {
		if r := recover(); r != nil {
			_ = db.Close()
			err = fmt.Errorf("failed to build the application: %v", r)
		}
	}()
	return app.NewOramaApp(log.NewNopLogger(), db, true, v, server.DefaultBaseappOptions(v)...), nil
}
