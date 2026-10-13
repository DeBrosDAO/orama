package runner

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

const (
	// DefaultWorkers is how many goroutines send transactions. Each worker sends to one node.
	DefaultWorkers = 32
	// DefaultDrain is how long a step waits for what it sent to be included.
	DefaultDrain = 45 * time.Second
	// DefaultSettle is how long the run waits between steps for the mempools to empty.
	DefaultSettle = 60 * time.Second
	// DefaultParallel bounds setup transactions in flight.
	DefaultParallel = 16
	// DefaultSeed is the string the bench accounts are derived from.
	DefaultSeed = "oramad-bench-localnet"
	// EnvironmentNote is printed with every result: the nodes and the generator share a machine.
	EnvironmentNote = "All validators and the load generator ran on this one machine, so these are localnet numbers of this machine, not of a network."
)

// Config is everything a run needs.
type Config struct {
	// Nodes are the CometBFT RPC addresses of the validators, e.g. tcp://127.0.0.1:31001.
	Nodes []string
	// FunderKeyFile holds the faucet signer's key (`localnet.sh funder-key`).
	FunderKeyFile string
	Seed          string
	Profile       profile.Profile
	Broadcast     broadcast.Mode
	Workers       int
	FeeMultiple   int64
	GasAdjust     uint64
	// CachePath is the shielded bundle cache file.
	CachePath string
	// ModuleDir is the chain module's root, where the bundle factory's crate lives.
	ModuleDir string
	// ProverBin, when set, is a built gen_bench to run instead of `cargo run`.
	ProverBin string
	Drain     time.Duration
	Settle    time.Duration
	Parallel  int
	// Log receives progress lines.
	Log io.Writer
}

// WithDefaults fills the fields left zero.
func (c Config) WithDefaults() Config {
	if c.Seed == "" {
		c.Seed = DefaultSeed
	}
	if c.Broadcast == "" {
		c.Broadcast = broadcast.ModeSync
	}
	if c.Workers == 0 {
		c.Workers = DefaultWorkers
	}
	if c.FeeMultiple == 0 {
		c.FeeMultiple = ops.DefaultFeeMultiple
	}
	if c.GasAdjust == 0 {
		c.GasAdjust = ops.DefaultGasAdjustPercent
	}
	if c.Drain == 0 {
		c.Drain = DefaultDrain
	}
	if c.Settle == 0 {
		c.Settle = DefaultSettle
	}
	if c.Parallel == 0 {
		c.Parallel = DefaultParallel
	}
	return c
}

// Validate checks the config.
func (c Config) Validate() error {
	if len(c.Nodes) < 2 {
		return fmt.Errorf("%d node addresses given; at least two are needed to compare app hashes across validators", len(c.Nodes))
	}
	if c.FunderKeyFile == "" {
		return errors.New("no funder key file; pass --funder-key-file (`localnet.sh funder-key` writes it)")
	}
	if _, err := broadcast.ParseMode(string(c.Broadcast)); err != nil {
		return err
	}
	if c.Workers < 1 || c.Parallel < 1 {
		return errors.New("workers and parallel must be at least 1")
	}
	if c.Broadcast == broadcast.ModeAsync {
		for _, t := range c.Profile.Types {
			for _, s := range t.Steps {
				if s.Mode == report.ModeClosed {
					return fmt.Errorf("type %s has a closed-loop step: -broadcast async hides refusals, so a refused transaction would hold its place in the window for ever; use sync", t.Type)
				}
			}
		}
	}
	return c.Profile.Validate()
}

func (c Config) logf(format string, args ...any) {
	if c.Log != nil {
		fmt.Fprintf(c.Log, "==> "+format+"\n", args...)
	}
}
