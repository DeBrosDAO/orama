package shielded

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"cosmossdk.io/math"
)

// Tip is the chain's head as the cache needs it.
type Tip struct {
	Height   int64
	TreeSize uint64
	// Root is the hex of the note tree's current root; empty for an empty tree.
	Root string
}

// Chain is what Build asks of the chain. The load generator implements it over a node.
type Chain interface {
	ChainID() string
	// GenesisHash is the hash of block 1.
	GenesisHash(ctx context.Context) (string, error)
	Params(ctx context.Context) (Params, error)
	// GasPrice is the price per gas unit a transfer's fee is sized for.
	GasPrice(ctx context.Context) (math.Int, error)
	Tip(ctx context.Context) (Tip, error)
	// SubmitShields signs one MsgShield per bundle with the bench accounts, sends them and waits
	// until all are included and executed.
	SubmitShields(ctx context.Context, bundles [][]byte) error
	// SignerAddress is the 20 address bytes, in hex, of bench account i.
	SignerAddress(signer int) (string, error)
	Blocks() BlockSource
}

// Prover makes bundles. Factory is the real one; tests give a fake.
type Prover interface {
	Shields(ctx context.Context, chainID string, jobs []ShieldJob) ([]Built, error)
	Spends(ctx context.Context, chainID string, leaves []string, expectRoot string, jobs []SpendJob) (Response, error)
}

var _ Prover = Factory{}

// Options are the settings of a Build.
type Options struct {
	Prover      Prover
	AccountSeed string
	Logf        func(format string, args ...any)
}

func (o Options) log(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Build makes a cache for plan on the chain. It proves and submits the shield bundles that create
// the notes the plan spends (the chain's tree changes), reads the tree back, proves the plan's
// transfers and unshields against it, and proves the plan's measured shields. The result is valid
// for this chain from the height of its last tree change until the anchor window passes.
func Build(ctx context.Context, ch Chain, plan Plan, opt Options) (*Cache, error) {
	params, err := ch.Params(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the shielded params: %w", err)
	}
	if err := plan.Validate(params); err != nil {
		return nil, err
	}
	pools, err := seedNotes(ctx, ch, plan, opt)
	if err != nil {
		return nil, err
	}
	genesis, err := ch.GenesisHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the genesis hash: %w", err)
	}
	cache := &Cache{
		Version: CacheVersion, ChainID: ch.ChainID(), GenesisHash: genesis, AccountSeed: opt.AccountSeed,
		AnchorWindow: params.AnchorWindow, CreatedAt: time.Now().UTC(),
	}
	if err := addSpends(ctx, ch, plan, params, pools, opt, cache); err != nil {
		return nil, err
	}
	if err := addShields(ctx, ch, plan, opt, cache); err != nil {
		return nil, err
	}
	return cache, nil
}

// seedNotes proves the seed shields, submits them, and returns each wallet's seed notes.
func seedNotes(ctx context.Context, ch Chain, plan Plan, opt Options) (map[uint32][]string, error) {
	jobs := plan.SeedJobs()
	pools := map[uint32][]string{}
	if len(jobs) == 0 {
		return pools, nil
	}
	opt.log("proving %d seed shield bundles for %d notes", len(jobs), plan.SeedNotes())
	built, err := opt.Prover.Shields(ctx, ch.ChainID(), jobs)
	if err != nil {
		return nil, fmt.Errorf("prove the seed shields: %w", err)
	}
	bundles := make([][]byte, len(built))
	for i, b := range built {
		if bundles[i], err = hex.DecodeString(b.Bundle); err != nil {
			return nil, fmt.Errorf("seed bundle %s is not hex: %w", b.ID, err)
		}
		for _, n := range b.Notes {
			if n.Value == NoteValue {
				pools[n.Account] = append(pools[n.Account], n.Note)
			}
		}
	}
	opt.log("submitting the seed shields")
	if err := ch.SubmitShields(ctx, bundles); err != nil {
		return nil, fmt.Errorf("the chain would not take the seed shields (does the node verify shielded bundles?): %w", err)
	}
	return pools, nil
}

// addSpends reads the tree back, proves the plan's transfers and unshields against it and adds them.
func addSpends(ctx context.Context, ch Chain, plan Plan, params Params, pools map[uint32][]string, opt Options, cache *Cache) error {
	if plan.SeedNotes() == 0 {
		return nil
	}
	tip, err := ch.Tip(ctx)
	if err != nil {
		return fmt.Errorf("read the chain's head: %w", err)
	}
	leaves, err := ScanLeaves(ctx, ch.Blocks(), 1, tip.Height)
	if err != nil {
		return fmt.Errorf("rebuild the note tree from the chain's blocks: %w", err)
	}
	if uint64(len(leaves.Commitments)) != tip.TreeSize {
		return fmt.Errorf("the blocks hold %d note commitments, the chain's tree holds %d: the scan missed a bundle", len(leaves.Commitments), tip.TreeSize)
	}
	price, err := ch.GasPrice(ctx)
	if err != nil {
		return fmt.Errorf("read the gas price: %w", err)
	}
	jobs, err := plan.SpendJobs(pools, price, params, ch.SignerAddress)
	if err != nil {
		return err
	}
	opt.log("proving %d spends against a tree of %d notes", len(jobs), tip.TreeSize)
	resp, err := opt.Prover.Spends(ctx, ch.ChainID(), leaves.Commitments, tip.Root, jobs)
	if err != nil {
		return fmt.Errorf("prove the spends: %w", err)
	}
	cache.Anchor, cache.AnchorHeight, cache.TreeSize = resp.Anchor, leaves.LastChange, tip.TreeSize
	for i, b := range resp.Built {
		entry := Entry{ID: b.ID, Kind: jobs[i].Kind, Actions: b.Actions, Bundle: b.Bundle, ValueBalance: b.ValueBalance, Account: Signerless}
		if jobs[i].Kind == KindUnshield {
			entry.Account = unshieldSigner(i, plan)
		}
		cache.Entries = append(cache.Entries, entry)
	}
	return nil
}

// unshieldSigner is the bench account an unshield job was bound to: the jobs after the transfers
// cycle through the signers in order.
func unshieldSigner(jobIndex int, plan Plan) int {
	transfers := 0
	for _, n := range plan.Transfers {
		transfers += n
	}
	return (jobIndex - transfers) % plan.Signers
}

// addShields proves the plan's measured shield bundles: one note each, anchored to the empty tree.
func addShields(ctx context.Context, ch Chain, plan Plan, opt Options, cache *Cache) error {
	if plan.Shields == 0 {
		return nil
	}
	jobs := make([]ShieldJob, plan.Shields)
	for i := range jobs {
		jobs[i] = ShieldJob{ID: fmt.Sprintf("shield-%d", i), Outputs: []OutputSpec{{Account: uint32(i % Wallets), Address: addressPayment, Value: ShieldValue}}}
	}
	opt.log("proving %d measured shields", len(jobs))
	built, err := opt.Prover.Shields(ctx, ch.ChainID(), jobs)
	if err != nil {
		return fmt.Errorf("prove the measured shields: %w", err)
	}
	for i, b := range built {
		cache.Entries = append(cache.Entries, Entry{
			ID: b.ID, Kind: EntryShield, Actions: b.Actions, Account: i % plan.Signers, Bundle: b.Bundle, ValueBalance: b.ValueBalance,
		})
	}
	return nil
}
