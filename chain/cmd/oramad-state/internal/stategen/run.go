package stategen

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"

	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/app"
)

// DefaultIAVLCache is the IAVL node cache the generator uses: twenty times oramad's own default.
const DefaultIAVLCache = 2_000_000

// blockSpacing is the time between the generator's blocks. It is short so the last of them is not
// in the future when the node starts; the node's first block carries the genesis time that follows.
const blockSpacing = time.Millisecond

// Config is one run of the generator.
type Config struct {
	// Home is a node home made by `oramad init` and a genesis for one validator.
	Home string
	Plan Plan
	// Pruning, when not empty, replaces app.toml's pruning strategy while the state is written. IAVL
	// prunes old versions at every tenth height under "everything", and that costs far more than
	// the writes, so the default is the home's own strategy, which keeps every version a run makes.
	Pruning string
	// IAVLCache is the IAVL node cache size while the state is written (DefaultIAVLCache when zero). A
	// node's own cache is small; the generator inserts into a tree far larger than it, and a node that
	// is not cached is a read from disk.
	IAVLCache int
	// Progress, when set, is called after every committed step; an error from it stops the run
	// there (the committed steps stay, and a later run resumes after them).
	Progress func(step, steps uint64) error
}

// Result is what a run left in the home.
type Result struct {
	Plan   Plan   `json:"plan"`
	Steps  uint64 `json:"steps"`
	Height int64  `json:"height"`
	// AppHash is the hash the last commit produced; the genesis file carries it as app_hash.
	AppHash string `json:"app_hash"`
	// Resumed is the height the run started from: zero for a new home.
	Resumed int64 `json:"resumed_from_height"`
	// Nullifiers is the count the committed x/shielded state holds.
	Nullifiers uint64 `json:"nullifiers_in_state"`
	Sizes      Sizes  `json:"sizes"`
	// ElapsedSeconds is the wall time of this run (a resumed run counts only its own steps).
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

// Run writes the plan into the home and prepares the home's genesis file to start a node on it. It
// continues where an earlier run stopped, and returns at once when the plan is already written.
func Run(cfg Config) (Result, error) {
	start := time.Now()
	res, err := execute(cfg)
	res.ElapsedSeconds = time.Since(start).Seconds()
	return res, err
}

func execute(cfg Config) (res Result, err error) {
	if err := cfg.Plan.Validate(); err != nil {
		return Result{}, err
	}
	if _, err := LoadCometConfig(cfg.Home); err != nil {
		// Read now, not after hours of writing: the last step needs it.
		return Result{}, fmt.Errorf("%w (run `oramad-state prepare` or `oramad init` first)", err)
	}
	st, err := beginState(cfg)
	if err != nil {
		return Result{}, err
	}
	a, err := OpenApp(cfg.Home, cfg.Pruning, cmp.Or(cfg.IAVLCache, DefaultIAVLCache))
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if closeErr := a.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close the application of %s: %w", cfg.Home, closeErr))
		}
	}()
	res = Result{Plan: cfg.Plan, Steps: cfg.Plan.Steps(), Resumed: a.LastBlockHeight()}
	if uint64(res.Resumed) > res.Steps {
		return res, fmt.Errorf("%s holds %d blocks, more than the %d steps of the plan: a node has run on it", cfg.Home, res.Resumed, res.Steps)
	}
	if st, err = startChain(a, cfg.Home, st); err != nil {
		return res, err
	}
	for step := uint64(a.LastBlockHeight()); step < res.Steps; step++ {
		if err := runStep(a, st, step); err != nil {
			return res, err
		}
		if cfg.Progress == nil {
			continue
		}
		if err := cfg.Progress(step+1, res.Steps); err != nil {
			return res, err
		}
	}
	return finish(a, cfg.Home, st, res)
}

// beginState loads the home's state file or starts one from the genesis file.
func beginState(cfg Config) (stateFile, error) {
	st, ok, err := loadState(cfg.Home)
	if err != nil {
		return st, err
	}
	if ok {
		if st.Fingerprint != cfg.Plan.Fingerprint() {
			return st, fmt.Errorf("%s was started with plan %+v; a resume must give the same plan (use a new home for %+v)",
				statePath(cfg.Home), st.Plan, cfg.Plan)
		}
		return st, nil
	}
	genesis, err := genutiltypes.AppGenesisFromFile(genesisPath(cfg.Home))
	if err != nil {
		return st, fmt.Errorf("failed to read the genesis of %s: %w", cfg.Home, err)
	}
	st = stateFile{Plan: cfg.Plan, Fingerprint: cfg.Plan.Fingerprint(), GenesisTime: genesis.GenesisTime.UTC(), ChainID: genesis.ChainID}
	return st, saveState(cfg.Home, st)
}

func genesisPath(home string) string { return filepath.Join(home, configDir, "genesis.json") }

// startChain runs InitChain on a home with no committed block: it loads the genesis file's app state
// and records the validators InitChain returns. A home that has blocks is left as it is. The
// nullifier database of an interrupted first step is emptied first, since genesis refuses a
// database that already holds records.
func startChain(a *app.OramaApp, home string, st stateFile) (stateFile, error) {
	if a.LastBlockHeight() > 0 {
		return st, nil
	}
	if err := a.NullifierStore().Reset(); err != nil {
		return st, fmt.Errorf("failed to empty the nullifier database of an unfinished first step: %w", err)
	}
	genesis, err := genutiltypes.AppGenesisFromFile(genesisPath(home))
	if err != nil {
		return st, fmt.Errorf("failed to read the genesis of %s: %w", home, err)
	}
	req, err := initChainRequest(genesis, st)
	if err != nil {
		return st, err
	}
	resp, err := a.InitChain(req)
	if err != nil {
		return st, fmt.Errorf("InitChain failed: %w", err)
	}
	st.Validators = st.Validators[:0]
	vals, err := cmttypes.PB2TM.ValidatorUpdates(resp.Validators)
	if err != nil {
		return st, fmt.Errorf("failed to read the validators InitChain returned: %w", err)
	}
	for _, v := range vals {
		st.Validators = append(st.Validators, validator{PubKey: v.PubKey.Bytes(), Power: v.VotingPower})
	}
	return st, saveState(home, st)
}

// initChainRequest is the request CometBFT would send for the genesis file.
func initChainRequest(genesis *genutiltypes.AppGenesis, st stateFile) (*abci.RequestInitChain, error) {
	doc, err := genesis.ToGenesisDoc()
	if err != nil {
		return nil, fmt.Errorf("the genesis file is not a valid genesis document: %w", err)
	}
	params := doc.ConsensusParams.ToProto()
	return &abci.RequestInitChain{
		ChainId:         genesis.ChainID,
		InitialHeight:   1,
		Time:            st.GenesisTime,
		AppStateBytes:   genesis.AppState,
		ConsensusParams: &params,
	}, nil
}

// runStep is one block: the empty block of the step's height, with the step's writes added to its
// state before the commit.
func runStep(a *app.OramaApp, st stateFile, step uint64) error {
	height := int64(step) + 1
	when := st.GenesisTime.Add(time.Duration(step+1) * blockSpacing)
	if _, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: when}); err != nil {
		return fmt.Errorf("FinalizeBlock failed at height %d: %w", height, err)
	}
	ctx := a.NewContextLegacy(false, cmtproto.Header{ChainID: st.ChainID, Height: height, Time: when})
	if err := writeStep(a, ctx, st.Plan, step); err != nil {
		return fmt.Errorf("step %d: %w", step, err)
	}
	// FinalizeBlock has already flushed its cache into the root store to compute the block's hash;
	// the step's writes are flushed the same way, or Commit would not see them.
	ctx.MultiStore().(storetypes.CacheMultiStore).Write()
	if _, err := a.Commit(); err != nil {
		return fmt.Errorf("commit failed at height %d: %w", height, err)
	}
	return nil
}
