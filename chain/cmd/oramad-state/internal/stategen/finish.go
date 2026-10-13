package stategen

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/chain/app"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"

	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
)

// Sizes are the bytes the home's databases take on disk.
type Sizes struct {
	ApplicationDB int64 `json:"application_db_bytes"`
	NullifierDB   int64 `json:"nullifier_db_bytes"`
	HomeData      int64 `json:"data_dir_bytes"`
}

// finish reads back what the commits left, rewrites the genesis file so a node starts at the next
// height on this state, and measures the databases.
func finish(a *app.OramaApp, home string, st stateFile, res Result) (Result, error) {
	res.Height = a.LastBlockHeight()
	res.AppHash = hex.EncodeToString(a.LastCommitID().Hash)
	ctx := a.NewContext(true)
	_, count, err := readAccumulator(ctx, a)
	if err != nil {
		return res, err
	}
	res.Nullifiers = count
	if err := patchGenesis(home, st, res.Height, a.LastCommitID().Hash); err != nil {
		return res, err
	}
	res.Sizes, err = MeasureSizes(home)
	return res, err
}

// patchGenesis makes the home's genesis file the one a node starts on the generated state with: the
// first block is the one after the last committed, the app hash is the last commit's, and the
// validators are the ones InitChain returned (CometBFT asks the application for them only on a chain
// with no blocks). Its time is after the generator's last block, which a node's first block must
// carry. The app state stays, so `oramad genesis validate` still reads the file. CometBFT's state
// database gets the state that genesis describes (saveCometState).
func patchGenesis(home string, st stateFile, height int64, appHash []byte) error {
	genesis, err := genutiltypes.AppGenesisFromFile(genesisPath(home))
	if err != nil {
		return fmt.Errorf("failed to read the genesis of %s: %w", home, err)
	}
	genesis.InitialHeight = height + 1
	genesis.AppHash = appHash
	genesis.GenesisTime = st.GenesisTime.Add(time.Duration(height+1) * blockSpacing)
	if genesis.Consensus == nil {
		return fmt.Errorf("the genesis file of %s has no consensus section", home)
	}
	genesis.Consensus.Validators = genesisValidators(st.Validators)
	if err := genesis.SaveAs(genesisPath(home)); err != nil {
		return fmt.Errorf("failed to write the genesis of %s: %w", home, err)
	}
	return saveCometState(home, genesis)
}

// MeasureSizes reports the on-disk size of a home's databases.
func MeasureSizes(home string) (Sizes, error) {
	var s Sizes
	var err error
	if s.ApplicationDB, err = DirSize(filepath.Join(home, dataDir, appDBName+".db")); err != nil {
		return s, err
	}
	if s.NullifierDB, err = DirSize(filepath.Join(home, dataDir, shieldedtypes.NullifierStoreFile+".db")); err != nil {
		return s, err
	}
	s.HomeData, err = DirSize(filepath.Join(home, dataDir))
	return s, err
}

// DirSize is the total size of the regular files under dir; a directory that does not exist is empty.
func DirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to measure %s: %w", dir, err)
	}
	return total, nil
}
