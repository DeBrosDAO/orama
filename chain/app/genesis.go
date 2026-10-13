package app

import "encoding/json"

// GenesisState is the app-wide genesis state: a map of raw JSON messages keyed by module name.
// Each module's InitGenesis/ExportGenesis reads and writes only its own entry.
type GenesisState map[string]json.RawMessage

// NewDefaultGenesisState returns the default genesis state, keyed by module name, as produced by
// every registered module's AppModuleBasic.DefaultGenesis.
func NewDefaultGenesisState(app *OramaApp) GenesisState {
	return app.DefaultGenesis()
}
