package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"

	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const (
	// DeclineMarginBlocks is how many blocks before the accept window closes
	// the runner declines a slot whose piece never arrived. Declining lets the
	// chain reassign the slot now instead of after the window lapses.
	DeclineMarginBlocks = 2
	// ReleaseGraceEpochs is how long a piece stays on disk after the chain
	// stops naming this node for its slot.
	ReleaseGraceEpochs = 1
	// MaxProofsPerTx bounds one MsgSubmitProofs.
	MaxProofsPerTx = 32
	// MaxBlocksPerStep bounds how far one step catches up, so a node far
	// behind still proves every step.
	MaxBlocksPerStep = 500
	// ReasonNotStored is the decline reason when no uploaded piece matches the slot root.
	ReasonNotStored = "piece not stored"
)

// eventAssigned is x/storage's event for a slot given to a node.
const eventAssigned = "storage_slot_assigned"

// Chain is the node the runner reads and the one it submits to. Submit signs
// with the node's hot key and returns after a block includes the transaction.
type Chain interface {
	LatestHeight(ctx context.Context) (int64, error)
	BlockEvents(ctx context.Context, height int64) ([]abci.Event, error)
	Params(ctx context.Context) (types.Params, error)
	Slot(ctx context.Context, dealID uint64, slot uint32) (types.Slot, error)
	CurrentEpoch(ctx context.Context) (uint64, error)
	Challenges(ctx context.Context, epoch uint64, nodeID string) ([]types.Challenge, error)
	Balance(ctx context.Context, addr string) (math.Int, error)
	Submit(ctx context.Context, msgs ...sdk.Msg) error
}

// Runner turns chain events for one node into accepts, declines, proofs and
// releases. Every step reads the chain state again, so an event it missed or
// a transaction that was dropped is handled on the next step.
type Runner struct {
	store       *Store
	chain       Chain
	nodeID      string
	signer      string
	statePath   string
	monitorPath string

	mu    sync.Mutex
	state runnerState
	// sweptEpoch is the last epoch sweep ran for. It is not persisted, so a
	// restart sweeps once.
	sweptEpoch uint64
	// accepted is set when this step accepted a slot.
	accepted bool
}

// Config names the node, its hot key, and where the runner keeps its cursor.
type Config struct {
	NodeID string
	// Signer is the node's hot key address. The chain refuses any other signer.
	Signer string
	// StatePath holds the block cursor and the slots waiting for a piece.
	StatePath string
	// MonitorPath is the status file the node report reads. Empty skips it.
	MonitorPath string
	// StartHeight is the first block read when StatePath does not exist yet,
	// and must then be at least 1. It is not used once the state exists:
	// the node's x/nodes registration height, since nothing is assigned to a
	// node before it registers.
	StartHeight int64
}

// NewRunner loads the cursor from cfg.StatePath, or starts at cfg.StartHeight.
func NewRunner(store *Store, chain Chain, cfg Config) (*Runner, error) {
	if store == nil || chain == nil {
		return nil, errors.New("runner needs a store and a chain")
	}
	if cfg.NodeID == "" || cfg.Signer == "" || cfg.StatePath == "" {
		return nil, errors.New("runner needs a node id, a signer, and a state path")
	}
	st, err := loadState(cfg.StatePath, cfg.StartHeight)
	if err != nil {
		return nil, err
	}
	return &Runner{
		store: store, chain: chain, nodeID: cfg.NodeID, signer: cfg.Signer,
		statePath: cfg.StatePath, monitorPath: cfg.MonitorPath, state: st,
	}, nil
}

// Assigned reports whether name is the hex piece root of a slot this node
// is waiting to receive. It is the upload filter for Retrieval.AcceptUploads.
func (r *Runner) Assigned(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.state.Pending {
		if p.Root != "" && p.Root == name {
			return true
		}
	}
	return false
}

// Step proves this epoch's challenges first, then reads at most
// MaxBlocksPerStep blocks past the cursor, settles waiting slots, and, once
// per epoch, releases slots the chain dropped. A failure in one part does not
// stop the others: a slot that cannot be accepted never costs a proof.
func (r *Runner) Step(ctx context.Context) error {
	epoch, err := r.chain.CurrentEpoch(ctx)
	if err != nil {
		return err
	}
	misses, answerErr := r.answer(ctx, epoch)
	r.accepted = false
	followErr := r.follow(ctx)
	if r.accepted && answerErr == nil {
		// An accept opens a challenge in the current epoch; answer it now
		// rather than one step later, which could fall after the epoch closes.
		misses, answerErr = r.answer(ctx, epoch)
	}
	return errors.Join(answerErr, followErr, r.sweepOnce(ctx, epoch), r.writeMonitor(ctx, misses))
}

// follow reads new blocks and settles the slots waiting for a piece.
func (r *Runner) follow(ctx context.Context) error {
	latest, err := r.chain.LatestHeight(ctx)
	if err != nil {
		return err
	}
	readErr := r.readBlocks(ctx, latest)
	params, err := r.chain.Params(ctx)
	if err != nil {
		return errors.Join(readErr, err)
	}
	return errors.Join(readErr, r.settlePending(ctx, latest, params))
}

// sweepOnce runs sweep the first time Step sees an epoch. Slots are released
// on epoch boundaries, so reading every binding more often buys nothing.
func (r *Runner) sweepOnce(ctx context.Context, epoch uint64) error {
	if r.sweptEpoch == epoch {
		return nil
	}
	if err := r.sweep(ctx, epoch); err != nil {
		return err
	}
	r.sweptEpoch = epoch
	return nil
}

func (r *Runner) readBlocks(ctx context.Context, latest int64) error {
	last := min(latest, r.cursor()+MaxBlocksPerStep)
	for h := r.cursor() + 1; h <= last; h++ {
		events, err := r.chain.BlockEvents(ctx, h)
		if err != nil {
			return fmt.Errorf("provider cursor is at %d: %w", h-1, err)
		}
		r.mu.Lock()
		changed := r.applyEvents(events)
		r.state.Height = h
		r.mu.Unlock()
		if changed || h == last {
			if err := r.save(); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyEvents records slots assigned to this node. Eviction, expiry and the
// outcome of a decision are read from the slot itself by settlePending and
// sweep, so an event missed here cannot leave a stale binding.
func (r *Runner) applyEvents(events []abci.Event) bool {
	changed := false
	for _, ev := range events {
		if ev.Type != eventAssigned {
			continue
		}
		dealID, slot, node, ok := slotEvent(ev)
		if ok && node == r.nodeID && r.state.addPending(dealID, slot) {
			changed = true
		}
	}
	return changed
}

func slotEvent(ev abci.Event) (uint64, uint32, string, bool) {
	var dealID uint64
	var slot uint32
	var node string
	var haveDeal, haveSlot bool
	for _, attr := range ev.Attributes {
		switch attr.Key {
		case "deal_id":
			v, err := strconv.ParseUint(attr.Value, 10, 64)
			haveDeal = err == nil && v > 0
			dealID = v
		case "slot":
			v, err := strconv.ParseUint(attr.Value, 10, 32)
			haveSlot = err == nil
			slot = uint32(v)
		case "node_id":
			node = attr.Value
		}
	}
	return dealID, slot, node, haveDeal && haveSlot && node != ""
}

func (r *Runner) cursor() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Height
}

func rootName(root []byte) string { return hex.EncodeToString(root) }

func slotKey(dealID uint64, slot uint32) string { return fmt.Sprintf("%d/%d", dealID, slot) }
