package stategen

import (
	"fmt"
	"path/filepath"

	cmtcfg "github.com/cometbft/cometbft/config"
	sm "github.com/cometbft/cometbft/state"
	"github.com/spf13/viper"

	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
)

const cometConfigFile = "config.toml"

// saveCometState writes CometBFT's own state for the chain the generated application state belongs
// to: the state of a chain whose genesis is the application's last committed block. CometBFT builds
// that state from the genesis file when it runs InitChain, but it never runs InitChain for an
// application that already has blocks, and it then finds no state at all. This is the state it would
// have saved: no block yet, the first one is the genesis file's initial height, the validators and
// the app hash are the file's.
func saveCometState(home string, genesis *genutiltypes.AppGenesis) error {
	doc, err := genesis.ToGenesisDoc()
	if err != nil {
		return fmt.Errorf("the genesis file is not a valid genesis document: %w", err)
	}
	state, err := sm.MakeGenesisState(doc)
	if err != nil {
		return fmt.Errorf("failed to make CometBFT's state from the genesis: %w", err)
	}
	cfg, err := LoadCometConfig(home)
	if err != nil {
		return err
	}
	db, err := cmtcfg.DefaultDBProvider(&cmtcfg.DBContext{ID: "state", Config: cfg})
	if err != nil {
		return fmt.Errorf("failed to open CometBFT's state database: %w", err)
	}
	defer db.Close()
	if err := sm.NewStore(db, sm.StoreOptions{}).Save(state); err != nil {
		return fmt.Errorf("failed to save CometBFT's state: %w", err)
	}
	return nil
}

// LoadCometConfig reads the home's config.toml over CometBFT's defaults, as the node does, so the
// state database is the one (backend and path) the node opens.
func LoadCometConfig(home string) (*cmtcfg.Config, error) {
	path := filepath.Join(home, configDir, cometConfigFile)
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	cfg := cmtcfg.DefaultConfig()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	cfg.SetRoot(home)
	return cfg, nil
}
