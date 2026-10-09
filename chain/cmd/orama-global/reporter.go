package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/reporter"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

const reporterInterval = 5 * time.Minute

type reporterFlags struct {
	rpc, home, votes string
	interval         time.Duration
	voteInterval     time.Duration
	chunk            int
}

func reporterCmd() *cobra.Command {
	var fl reporterFlags
	cmd := &cobra.Command{
		Use:   "reporter",
		Short: "Report each closed epoch's relay bandwidth and uptime to x/relay from this directory authority's votes",
		Long: `reporter runs on a directory-authority host. Each time x/emission closes an epoch it
reads this authority's archived votes (<votes>/*.vote) for that epoch's span and sends
x/relay a chunked MsgReportEpoch of {rsa_fingerprint, ed25519_id, consensus_weight, flags,
uptime_fraction} per registered relay, with the inputs_root of the entries.
Weight is the median Measured= bandwidth of the votes that list the relay Running;
an unmeasured relay weighs zero. Uptime is the share of the epoch's votes that list it
Running. An epoch whose votes the archive holds less than four fifths of is not reported.
Relays that belong to this reporter's operator are left out.
<home>/hot-key is the signing key, created on first start (mode 0600); its address must be
in x/relay's reporter set. <home>/operator holds the operator address and
<home>/authority-id the authority's 40-hex v3 identity (the dir-source line of its votes).
<home>/state.json and <home>/monitor.json report what the last pass did.
x/relay takes a report for an epoch only while the chain is in the epoch after it, and settles
the epoch in the first block after that; a pass that finds the window over (or the epoch
settled) drops the epoch with an error instead of retrying it, so --interval must be much
shorter than an epoch. <home>/authority-id is the v3_ident of this authority in the Orama Tor
network file (tor-network.json), where the authorities are listed with their nicknames.
Another party recomputes the observations from the same votes with reporter.LoadVotes and
Observe; the entries sent are those narrowed by the registry as it stood when they were chosen.`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runReporter(cmd.Context(), fl) },
	}
	f := cmd.Flags()
	f.StringVar(&fl.rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	f.StringVar(&fl.home, "home", ".", "Reporter state directory")
	f.StringVar(&fl.votes, "votes-dir", "", "Directory of archived votes (default <home>/votes)")
	f.DurationVar(&fl.interval, "interval", reporterInterval, "Time between passes")
	f.DurationVar(&fl.voteInterval, "vote-interval", reporter.DefaultVoteInterval, "The authority's voting interval")
	f.IntVar(&fl.chunk, "chunk-entries", reporter.DefaultChunkEntries, "Relays per MsgReportEpoch")
	return cmd
}

func runReporter(ctx context.Context, fl reporterFlags) error {
	if fl.interval <= 0 {
		return errors.New("--interval must be positive")
	}
	key, created, err := loadOrCreateHotKey(filepath.Join(fl.home, "hot-key"))
	if err != nil {
		return err
	}
	if created {
		slog.Info("created the reporter key; add its address to x/relay's reporter set and fund it", "address", key.Address)
	}
	cfg, err := reporterConfig(fl, key.Address)
	if err != nil {
		return err
	}
	client, err := node.DialWith(fl.rpc, relaytypes.RegisterInterfaces)
	if err != nil {
		return err
	}
	chain, err := reporter.NewNodeChain(client, key)
	if err != nil {
		return err
	}
	r, err := reporter.NewRunner(cfg, chain)
	if err != nil {
		return err
	}
	tick := time.NewTicker(fl.interval)
	defer tick.Stop()
	for {
		epochs, err := r.Step(ctx)
		for _, epoch := range epochs {
			slog.Info("reported epoch", "epoch", epoch)
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("reporter pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func reporterConfig(fl reporterFlags, address string) (reporter.Config, error) {
	operator, err := readOneLine(filepath.Join(fl.home, "operator"), "the operator address this reporter runs for")
	if err != nil {
		return reporter.Config{}, err
	}
	idText, err := readOneLine(filepath.Join(fl.home, "authority-id"), "the directory authority's 40-hex v3 identity")
	if err != nil {
		return reporter.Config{}, err
	}
	id, err := hex.DecodeString(idText)
	if err != nil || len(id) != len(reporter.Config{}.Authority) {
		return reporter.Config{}, fmt.Errorf("%s is not a 40-hex v3 identity", filepath.Join(fl.home, "authority-id"))
	}
	votes := fl.votes
	if votes == "" {
		votes = filepath.Join(fl.home, "votes")
	}
	cfg := reporter.Config{
		Home: fl.home, VotesDir: votes, Reporter: address, Operator: operator,
		VoteInterval: fl.voteInterval, ChunkEntries: fl.chunk,
	}
	copy(cfg.Authority[:], id)
	return cfg, cfg.Validate()
}
