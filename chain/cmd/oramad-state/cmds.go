package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/measure"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/stategen"
)

// defaultOramad is where `make build-oramad-state` puts the oramad the measurements drive.
const defaultOramad = "build/oramad-state-oramad"

// stategenGCPercent lets the generator's heap grow to three times what is live before a collection:
// the IAVL node cache is most of it, and scanning it on every cycle was a third of the run.
const stategenGCPercent = 200

// common are the flags most commands share.
type common struct {
	oramad  string
	home    string
	rpcPort int
	p2pPort int
	work    string
}

func (c *common) register(fs *flag.FlagSet, ports bool) {
	fs.StringVar(&c.oramad, "oramad", defaultOramad, "path of the oramad binary to drive")
	fs.StringVar(&c.home, "home", "", "the node home (required)")
	if ports {
		fs.IntVar(&c.rpcPort, "rpc-port", measure.DefaultRPCPort, "RPC port of the node the command starts")
		fs.IntVar(&c.p2pPort, "p2p-port", measure.DefaultP2PPort, "P2P port of the node the command starts")
	}
}

func (c *common) registerWork(fs *flag.FlagSet) {
	fs.StringVar(&c.work, "work", "", "directory for the files the command makes (default: a temporary one, removed afterwards)")
}

func (c common) oramadBin() measure.Oramad {
	return measure.Oramad{Bin: c.oramad}
}

func (c common) needHome() error {
	if c.home == "" {
		return errors.New("--home is required")
	}
	return nil
}

// workDir is the directory a command writes into, and the function that removes it when the command
// made it itself.
func (c common) workDir() (string, func(), error) {
	if c.work != "" {
		return c.work, func() {}, os.MkdirAll(c.work, 0o700)
	}
	dir, err := os.MkdirTemp("", "oramad-state-")
	return dir, func() { _ = os.RemoveAll(dir) }, err
}

func cmdPrepare(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("prepare")
	c.register(fs, false)
	chainID := fs.String("chain-id", measure.DefaultChainID, "chain id; it must be a localnet id, orama-[<name>-]localnet-<id>")
	timeoutCommit := fs.String("timeout-commit", "", "CometBFT's wait between blocks, e.g. 50ms (default: oramad's 5s)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	if err := measure.Prepare(ctx, c.oramadBin(), c.home, *chainID, *timeoutCommit); err != nil {
		return nil, err
	}
	return map[string]string{"home": c.home, "chain_id": *chainID}, nil
}

func cmdStategen(_ context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("stategen")
	c.register(fs, false)
	var nullifiers, assets, keys count
	fs.Var(&nullifiers, "nullifiers", "spent nullifiers to write (target 10M)")
	fs.Var(&assets, "assets", "decompressed cNFT assets to write (target 1M)")
	fs.Var(&keys, "keys", "filler IAVL keys to write, as accounts of 2 keys (target 100M)")
	seed := fs.Uint64("seed", 1, "seed of the derived identifiers; the same plan and seed write the same state")
	pruning := fs.String("pruning", "", "pruning strategy to use while writing (default: the home's app.toml)")
	profile := fs.String("cpuprofile", "", "write a CPU profile of the run to this file")
	cache := fs.Int("iavl-cache", stategen.DefaultIAVLCache, "IAVL node cache size while writing")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	stop, err := startProfile(*profile)
	if err != nil {
		return nil, err
	}
	defer stop()
	debug.SetGCPercent(stategenGCPercent)
	plan := stategen.Plan{Seed: *seed, Nullifiers: uint64(nullifiers), Assets: uint64(assets), Keys: uint64(keys)}
	return stategen.Run(stategen.Config{Home: c.home, Plan: plan, Pruning: *pruning, IAVLCache: *cache, Progress: progress(logf, plan.Steps())})
}

// progress logs every step, or every hundredth of the run when it has many, with the time spent and
// the time the rest should take at the rate so far.
func progress(logf func(string, ...any), steps uint64) func(step, steps uint64) error {
	every := max(steps/100, 1)
	start := time.Now()
	return func(step, steps uint64) error {
		if step%every != 0 && step != steps {
			return nil
		}
		spent := time.Since(start)
		left := time.Duration(float64(spent) / float64(step) * float64(steps-step))
		logf("stategen: step %d of %d committed after %s, about %s to go", step, steps, spent.Round(time.Second), left.Round(time.Second))
		return nil
	}
}

// startProfile starts a CPU profile into path ("" is none) and returns what stops it.
func startProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create the profile %s: %w", path, err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to start the CPU profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

func cmdBoot(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("boot")
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	o := c.oramadBin()
	if err := o.CheckBinary(); err != nil {
		return nil, err
	}
	return measure.Boot(ctx, o, c.home, c.rpcPort, c.p2pPort)
}

func cmdSnapshot(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("snapshot")
	c.register(fs, false)
	c.registerWork(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	work, cleanup, err := c.workDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return measure.Snapshot(ctx, c.oramadBin(), c.home, work)
}

func cmdExport(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("export")
	c.register(fs, false)
	c.registerWork(fs)
	keep := fs.Bool("keep", false, "keep the exported genesis document in --work")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	work, cleanup, err := c.workDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return measure.Export(ctx, c.oramadBin(), c.home, work, *keep)
}

func cmdIndexer(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("indexer")
	c.register(fs, true)
	blocks := fs.Int64("blocks", 5000, "blocks of history the follower catches up on")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := c.needHome(); err != nil {
		return nil, err
	}
	if *blocks < 1 {
		return nil, errors.New("--blocks must be at least 1")
	}
	return measure.Indexer(ctx, c.oramadBin(), c.home, *blocks, c.rpcPort, c.p2pPort)
}

func cmdPrune(ctx context.Context, args []string, logf func(string, ...any)) (any, error) {
	var c common
	fs := newFlags("prune")
	c.registerWork(fs)
	cfg := measure.PruneConfig{}
	fs.Int64Var(&cfg.Blocks, "blocks", 40_000, "blocks in the store (the chain's window is 241920; use more blocks than the window)")
	fs.Int64Var(&cfg.Window, "window", 20_000, "retention window in blocks the retain height uses (the chain's is 241920)")
	fs.Int64Var(&cfg.LastArchived, "last-archived", 0, "height x/archive reports archived (default: the tip)")
	fs.IntVar(&cfg.TxsPerBlock, "txs", measure.DefaultPruneTxs, "transactions per block")
	fs.IntVar(&cfg.TxBytes, "tx-bytes", measure.DefaultPruneTxBytes, "bytes per transaction")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	work, cleanup, err := c.workDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cfg.WorkDir = filepath.Clean(work)
	logf("prune: building %d blocks", cfg.Blocks)
	return measure.Prune(ctx, cfg)
}
