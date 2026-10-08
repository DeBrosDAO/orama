package reporter

import (
	"context"
	"errors"
	"fmt"
	"time"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// Epoch is an emission epoch as the reporter needs it.
type Epoch struct {
	Number uint64
	Start  time.Time
}

// Chain is what the reporter reads and writes on the chain.
type Chain interface {
	// CurrentEpoch is the emission epoch in progress.
	CurrentEpoch(ctx context.Context) (Epoch, error)
	// Relay is the registered relay with this RSA fingerprint.
	Relay(ctx context.Context, fingerprint []byte) (relaytypes.Relay, bool, error)
	// EpochSettled reports whether x/relay has settled the epoch, after which
	// it takes no more reports for it.
	EpochSettled(ctx context.Context, epoch uint64) (bool, error)
	// Submit signs msg with the reporter key and waits for its inclusion.
	Submit(ctx context.Context, msg *relaytypes.MsgReportEpoch) error
}

// Config is one reporter's identity and where its inputs are.
type Config struct {
	// Home holds state.json, monitor.json and the report being sent.
	Home string
	// VotesDir holds the archived votes (VoteSuffix files).
	VotesDir string
	// Reporter is the bech32 address of the reporter key; x/relay accepts
	// reports only from its governance-registered set.
	Reporter string
	// Operator is the bech32 address of the operator that runs this reporter.
	// Relays registered to it are never reported by it, so an operator that
	// runs both a dirauth and relays does not vouch for its own relays. This is
	// the honest reporter's rule, not one the chain enforces: a reporter that
	// signs without this binary is held only by the median across reporters.
	Operator string
	// Authority is the v3 identity of the directory authority this reporter
	// speaks for: only its votes are read.
	Authority    [fingerprintLen]byte
	VoteInterval time.Duration
	// ChunkEntries is the relays per message; zero is DefaultChunkEntries.
	ChunkEntries int
}

// Validate checks a configuration at the boundary.
func (c Config) Validate() error {
	switch {
	case c.Home == "" || c.VotesDir == "":
		return errors.New("reporter needs a home and a votes directory")
	case c.Reporter == "":
		return errors.New("reporter needs the address of its signing key")
	case c.Operator == "":
		return errors.New("reporter needs its operator address, to leave that operator's relays out of its reports")
	case c.Authority == [fingerprintLen]byte{}:
		return errors.New("reporter needs the identity of the directory authority it speaks for")
	case c.VoteInterval <= 0:
		return errors.New("vote interval must be positive")
	case c.ChunkEntries < 0 || c.ChunkEntries > relaytypes.MaxEntriesPerChunk:
		return fmt.Errorf("chunk entries %d is outside 0..%d", c.ChunkEntries, relaytypes.MaxEntriesPerChunk)
	}
	return nil
}

// ErrEpochMissed means epochs closed while no pass was running, so their spans
// are unknown and they cannot be reported. The loop logs it and carries on from
// the epoch now in progress.
var ErrEpochMissed = errors.New("epochs closed between two passes")

// ErrEpochSettled means x/relay settled the epoch before this reporter's report
// reached it, so it takes none.
var ErrEpochSettled = errors.New("the epoch is already settled on chain and takes no report")

// Runner reports each closed epoch once.
type Runner struct {
	cfg   Config
	chain Chain
}

// NewRunner checks cfg and pairs it with a chain.
func NewRunner(cfg Config, chain Chain) (*Runner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if chain == nil {
		return nil, errors.New("reporter needs a chain")
	}
	var err error
	if cfg.Operator, err = relaytypes.CanonicalAddress(cfg.Operator); err != nil {
		return nil, fmt.Errorf("reporter operator: %w", err)
	}
	if cfg.Reporter, err = relaytypes.CanonicalAddress(cfg.Reporter); err != nil {
		return nil, fmt.Errorf("reporter key: %w", err)
	}
	if cfg.ChunkEntries == 0 {
		cfg.ChunkEntries = DefaultChunkEntries
	}
	return &Runner{cfg: cfg, chain: chain}, nil
}

// Step is one pass. It notes the epoch boundary if one passed, then reports
// every closed epoch still owed, and returns the epochs the chain took in full.
//
// The boundary is saved before any report is tried, so a report that keeps
// failing (an archive still filling in) does not cost the epoch after it its
// span. A pass that fails before the chain took the whole report leaves that
// epoch owed; the next pass sends the same messages again, which the chain
// takes as a repeat.
func (r *Runner) Step(ctx context.Context) ([]uint64, error) {
	cur, err := r.chain.CurrentEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the current epoch: %w", err)
	}
	st, err := loadState(r.cfg.Home)
	if err != nil {
		return nil, err
	}
	st, err = observe(st, cur)
	missed := errors.Is(err, ErrEpochMissed)
	if err != nil && !missed {
		return nil, err
	}
	if serr := saveState(r.cfg.Home, st); serr != nil {
		return nil, errors.Join(err, serr)
	}
	reported, rerr := r.reportDue(ctx, &st)
	return reported, errors.Join(err, rerr)
}

// observe folds the epoch now in progress into the state: a boundary one epoch
// on makes the epoch that was in progress due, with its span. A longer jump
// loses the spans in between, which it returns as ErrEpochMissed with the state
// already moved on. A chain behind the state is an error and no state.
func observe(st State, cur Epoch) (State, error) {
	var err error
	switch {
	case st.Seen == 0:
	case cur.Number < st.Seen:
		return st, fmt.Errorf("the chain is at epoch %d, behind the epoch %d this reporter saw: is it the same chain? After a chain reset, remove the reporter's state.json", cur.Number, st.Seen)
	case cur.Number == st.Seen:
		return st, nil
	case cur.Number == st.Seen+1:
		st.Due = append(st.Due, Due{Epoch: st.Seen, FromUnixNano: st.SeenStartUnixNano, ToUnixNano: cur.Start.UnixNano()})
	default:
		err = fmt.Errorf("%w: the span of epochs %d to %d is lost", ErrEpochMissed, st.Seen, cur.Number-1)
	}
	st.Seen, st.SeenStartUnixNano = cur.Number, cur.Start.UnixNano()
	return st, err
}
