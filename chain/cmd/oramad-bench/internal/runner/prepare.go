package runner

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
)

// Prepare builds the shielded bundle cache the profile needs and writes it to cfg.CachePath. It
// changes the chain: it funds the bench accounts and shields the notes the cached transfers and
// unshields spend. The cache is valid for this chain from the height it records, so it is made
// against the chain the run will use, shortly before the run.
func Prepare(ctx context.Context, cfg Config) (*shielded.Cache, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.CachePath == "" {
		return nil, fmt.Errorf("no cache path given")
	}
	plan := cfg.Profile.ShieldedPlan(cfg.Profile.Accounts)
	if plan.SeedNotes() == 0 && plan.Shields == 0 {
		cfg.logf("profile %s runs no shielded type; there is nothing to prepare", cfg.Profile.Name)
		return nil, nil
	}
	accounts, err := account.DeriveN(cfg.Seed, cfg.Profile.Accounts)
	if err != nil {
		return nil, err
	}
	funder, err := LoadFunder(cfg.FunderKeyFile)
	if err != nil {
		return nil, err
	}
	cluster, err := Dial(ctx, cfg.Nodes, cfg.FeeMultiple, cfg.GasAdjust)
	if err != nil {
		return nil, err
	}
	cfg.logf("funding the bench accounts")
	if err := fundAccounts(ctx, cluster.Chain, funder, accounts, cfg.logf); err != nil {
		return nil, err
	}
	factory := shielded.Factory{Dir: filepath.Join(cfg.ModuleDir, shielded.WalletCrate), Binary: cfg.ProverBin, Progress: cfg.Log}
	sc := &shieldedChain{cluster: cluster, accounts: accounts, parallel: cfg.Parallel}
	cache, err := shielded.Build(ctx, sc, plan, shielded.Options{Prover: factory, AccountSeed: cfg.Seed, Logf: cfg.logf})
	if err != nil {
		return nil, err
	}
	if err := cache.Save(cfg.CachePath); err != nil {
		return nil, err
	}
	cfg.logf("shielded cache written to %s: %d bundles, valid for %d blocks from height %d", cfg.CachePath, len(cache.Entries), cache.AnchorWindow, cache.AnchorHeight)
	return cache, nil
}
