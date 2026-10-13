package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/runner"
)

const (
	// localnetRPCBase and localnetPortStep are scripts/localnet/localnet.sh's port scheme: node i's
	// RPC is on 31001 + 10*i.
	localnetRPCBase  = 31001
	localnetPortStep = 10
	defaultNodes     = 4
	defaultCache     = "scripts/localnet/.localnet/bench-shielded.json"
	defaultOut       = "scripts/localnet/.localnet/bench-report.json"
)

// options are the command line of `run` and `prepare`.
type options struct {
	cfg     runner.Config
	out     string
	profile string
	types   string
	nodes   string
	count   int
}

// parseOptions reads the flags shared by `run` and `prepare`.
func parseOptions(name string, args []string, errOut io.Writer) (options, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o options
	var mode string
	fs.StringVar(&o.profile, "profile", "smoke", "what to run: smoke or full")
	fs.StringVar(&o.types, "types", "", "comma-separated transaction types to run (default: all the profile's)")
	fs.StringVar(&o.nodes, "nodes", "", "comma-separated validator RPC addresses (default: the localnet's, see -localnet-nodes)")
	fs.IntVar(&o.count, "localnet-nodes", defaultNodes, "how many validators of scripts/localnet to talk to when -nodes is not given")
	fs.StringVar(&o.cfg.FunderKeyFile, "funder-key-file", "", "file holding the faucet signer's key (`localnet.sh funder-key`)")
	fs.StringVar(&o.cfg.Seed, "seed", "", "string the bench accounts are derived from")
	fs.StringVar(&mode, "broadcast", "sync", "broadcast_tx_sync (sees refusals) or broadcast_tx_async")
	fs.IntVar(&o.cfg.Workers, "workers", 0, "sender goroutines (default 32)")
	fs.Int64Var(&o.cfg.FeeMultiple, "fee-multiple", 0, fmt.Sprintf("offer this many times the base fee per gas (default %d)", ops.DefaultFeeMultiple))
	fs.Uint64Var(&o.cfg.GasAdjust, "gas-adjust", 0, fmt.Sprintf("gas limit as a percentage of the simulated gas (default %d)", ops.DefaultGasAdjustPercent))
	fs.StringVar(&o.cfg.CachePath, "cache", defaultCache, "shielded bundle cache file")
	fs.StringVar(&o.cfg.ModuleDir, "module-dir", ".", "the chain module root (where x/shielded/wallet lives)")
	fs.StringVar(&o.cfg.ProverBin, "prover-bin", "", "a built gen_bench to run instead of `cargo run`")
	fs.StringVar(&o.out, "out", defaultOut, "where to write the JSON report")
	fs.DurationVar(&o.cfg.Drain, "drain", 0, fmt.Sprintf("how long a step waits for its transactions to land (default %s)", runner.DefaultDrain))
	fs.DurationVar(&o.cfg.Settle, "settle", 0, fmt.Sprintf("how long to wait between steps for empty mempools (default %s)", runner.DefaultSettle))
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	m, err := broadcast.ParseMode(mode)
	if err != nil {
		return options{}, err
	}
	o.cfg.Broadcast = m
	return o, o.resolve()
}

// resolve turns the raw flags into the config: the profile, the type filter and the node list.
func (o *options) resolve() error {
	p, err := profile.ByName(o.profile)
	if err != nil {
		return err
	}
	if o.types != "" {
		if p, err = p.Only(splitList(o.types)); err != nil {
			return err
		}
	}
	o.cfg.Profile = p
	o.cfg.Nodes = splitList(o.nodes)
	if len(o.cfg.Nodes) == 0 {
		if o.count < 2 {
			return fmt.Errorf("-localnet-nodes %d: at least two validators are needed to compare app hashes", o.count)
		}
		for i := 0; i < o.count; i++ {
			o.cfg.Nodes = append(o.cfg.Nodes, fmt.Sprintf("tcp://127.0.0.1:%d", localnetRPCBase+i*localnetPortStep))
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
