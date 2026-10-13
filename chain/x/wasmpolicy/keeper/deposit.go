package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// FeesKeeper is the part of x/fees the state-deposit ledger uses. ReleaseDeposit and
// ReleaseDepositPart refund 99% to the payer's earnings and burn 1%.
type FeesKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
	ReleaseDeposit(ctx context.Context, id string) (refund, burn math.Int, err error)
	TopUpDeposit(ctx context.Context, id string, extra math.Int) error
	ReleaseDepositPart(ctx context.Context, id string, part math.Int) (refund, burn math.Int, err error)
	GetDeposit(ctx context.Context, id string) (feestypes.Deposit, error)
	// BurnFromBalance destroys amount of from's bank balance (never its earnings) and refuses,
	// moving nothing, when that balance is short. The code fee uses it.
	BurnFromBalance(ctx context.Context, from sdk.AccAddress, amount math.Int, reason string) error
}

// ApplyStateDelta settles one contract call's net change of stored bytes.
//
// Growth locks growth * deposit_per_byte from payer, in that payer's chunk of the contract: an
// existing chunk (found by the (contract, payer) index, so the cost does not depend on how many
// other payers the contract has) is topped up, a new payer gets a new chunk that also locks
// chunk_overhead_bytes, and there is no limit on payers, so a stranger can never take a slot another
// account needs. Growth
// past max_deposit_per_tx for the transaction is refused. A shrink first releases the shrinking
// payer's own chunk, so however many chunks other payers added after it the payer's refund is never
// delayed by them, then chunks newest created first, each refunded to the payer who locked it, and at
// most max_release_rows of those rows per call: the
// bytes it did not reach are carried and released by the contract's next calls, which work off the
// carry before they charge anything new. Only bytes that were charged can be refunded: a contract's first bytes (for example state
// present at genesis) were never charged, so a shrink past the charged total releases the
// charged total and no more.
func (k Keeper) ApplyStateDelta(ctx context.Context, contract, payer sdk.AccAddress, grew, shrank uint64) error {
	if grew <= shrank {
		return k.refund(ctx, contract, payer, shrank-grew)
	}
	// Work off the carry first, so a payer this call adds is not the newest row it releases.
	if err := k.refund(ctx, contract, payer, 0); err != nil {
		return err
	}
	return k.charge(ctx, contract, payer, grew-shrank)
}

func (k Keeper) charge(ctx context.Context, contract, payer sdk.AccAddress, n uint64) error {
	if payer.Empty() {
		return types.ErrDepositPayer.Wrapf("contract %s grew by %d bytes", contract, n)
	}
	limits, err := k.Limits.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load deposit limits: %w", err)
	}
	seq, err := k.PayerChunk.Get(ctx, collections.Join(contract.Bytes(), payer.Bytes()))
	if errors.Is(err, collections.ErrNotFound) {
		return k.newChunk(ctx, contract, payer, n, limits)
	}
	if err != nil {
		return fmt.Errorf("failed to find %s's deposit chunk of %s: %w", payer, contract, err)
	}
	c, err := k.Chunks.Get(ctx, collections.Join(contract.Bytes(), seq))
	if err != nil {
		return fmt.Errorf("failed to load deposit chunk %d of %s: %w", seq, contract, err)
	}
	return k.topUpChunk(ctx, contract, c, n, limits)
}

// topUpChunk adds n bytes to payer's existing chunk of contract: one payer is one deposit row.
func (k Keeper) topUpChunk(ctx context.Context, contract sdk.AccAddress, c types.DepositChunk, n uint64, limits types.Limits) error {
	extra := math.NewIntFromUint64(n).Mul(c.PerByte)
	if err := spendBudget(ctx, extra, limits); err != nil {
		return err
	}
	if err := k.fees.TopUpDeposit(ctx, c.DepositID, extra); err != nil {
		return fmt.Errorf("state deposit for %d new bytes of contract %s: %w", n, contract, err)
	}
	c.Bytes += n
	if err := k.Chunks.Set(ctx, collections.Join(contract.Bytes(), c.Seq), c); err != nil {
		return fmt.Errorf("failed to update deposit chunk %s: %w", c.DepositID, err)
	}
	if err := k.addLocked(ctx, contract, 0, extra); err != nil {
		return err
	}
	return k.addBytes(ctx, contract, n)
}

func (k Keeper) newChunk(ctx context.Context, contract, payer sdk.AccAddress, n uint64, limits types.Limits) error {
	perByte, err := k.DepositPerByte.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load deposit_per_byte: %w", err)
	}
	seq, err := k.NextChunk.Next(ctx)
	if err != nil {
		return fmt.Errorf("failed to take a deposit chunk sequence: %w", err)
	}
	chunk := types.DepositChunk{
		Contract: contract.String(), Seq: seq, Payer: payer.String(), Bytes: n, Overhead: limits.ChunkOverheadBytes, PerByte: perByte,
		DepositID: types.DepositID(contract.String(), seq),
	}
	if err := spendBudget(ctx, chunk.Amount(), limits); err != nil {
		return err
	}
	if err := k.fees.LockDeposit(ctx, payer, chunk.DepositID, chunk.Amount()); err != nil {
		return fmt.Errorf("state deposit for %d new bytes of contract %s: %w", n, contract, err)
	}
	if err := k.Chunks.Set(ctx, collections.Join(contract.Bytes(), seq), chunk); err != nil {
		return fmt.Errorf("failed to record deposit chunk %s: %w", chunk.DepositID, err)
	}
	if err := k.PayerChunk.Set(ctx, collections.Join(contract.Bytes(), payer.Bytes()), seq); err != nil {
		return fmt.Errorf("failed to index deposit chunk %s by payer: %w", chunk.DepositID, err)
	}
	if err := k.addLocked(ctx, contract, 1, chunk.Amount()); err != nil {
		return err
	}
	return k.addBytes(ctx, contract, n)
}

// spendBudget adds amount to the transaction's deposit budget and refuses to pass max_deposit_per_tx.
// Outside a transaction there is no budget and nothing to refuse.
func spendBudget(ctx context.Context, amount math.Int, limits types.Limits) error {
	b := types.DepositBudgetFrom(ctx)
	if b == nil {
		return nil
	}
	total := b.Locked.Add(amount)
	if total.GT(limits.MaxDepositPerTx) {
		return types.ErrDepositCap.Wrapf("%s of %s", total, limits.MaxDepositPerTx)
	}
	b.Locked = total
	return nil
}

// refund releases n freed bytes plus the contract's carry. The payer's own chunk goes first, up to n;
// the rest goes newest chunk first and at most max_release_rows rows. What it did not reach becomes
// the new carry.
func (k Keeper) refund(ctx context.Context, contract, payer sdk.AccAddress, n uint64) error {
	carry, err := k.carried(ctx, contract)
	if err != nil {
		return err
	}
	if n+carry == 0 {
		return nil
	}
	held, err := k.contractBytes(ctx, contract)
	if err != nil {
		return err
	}
	own, err := k.releaseOwn(ctx, contract, payer, min(n, held))
	if err != nil {
		return err
	}
	n -= own
	held -= own
	n = min(n+carry, held)
	if n == 0 {
		return k.setCarry(ctx, contract, 0)
	}
	released, err := k.releaseNewest(ctx, contract, n)
	if err != nil {
		return err
	}
	return k.setCarry(ctx, contract, n-released)
}

// releaseOwn releases up to n bytes of payer's own chunk of contract and returns how many it released.
func (k Keeper) releaseOwn(ctx context.Context, contract, payer sdk.AccAddress, n uint64) (uint64, error) {
	if n == 0 || payer.Empty() {
		return 0, nil
	}
	seq, err := k.PayerChunk.Get(ctx, collections.Join(contract.Bytes(), payer.Bytes()))
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to find %s's deposit chunk of %s: %w", payer, contract, err)
	}
	c, err := k.Chunks.Get(ctx, collections.Join(contract.Bytes(), seq))
	if err != nil {
		return 0, fmt.Errorf("failed to load deposit chunk %d of %s: %w", seq, contract, err)
	}
	released := min(n, c.Bytes)
	if err := k.releaseChunk(ctx, contract, c, released); err != nil {
		return 0, err
	}
	return released, k.subBytes(ctx, contract, released)
}

// releaseNewest releases n bytes from the contract's newest chunks, at most max_release_rows rows,
// and returns how many bytes it released.
func (k Keeper) releaseNewest(ctx context.Context, contract sdk.AccAddress, n uint64) (uint64, error) {
	limits, err := k.Limits.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load deposit limits: %w", err)
	}
	rng := collections.NewPrefixedPairRange[[]byte, uint64](contract.Bytes()).Descending()
	var rows []types.DepositChunk
	remaining := n
	err = k.Chunks.Walk(ctx, rng, func(_ collections.Pair[[]byte, uint64], c types.DepositChunk) (bool, error) {
		rows = append(rows, c)
		if c.Bytes >= remaining || uint64(len(rows)) >= limits.MaxReleaseRows {
			return true, nil
		}
		remaining -= c.Bytes
		return false, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to walk deposit chunks of %s: %w", contract, err)
	}
	remaining = n
	for _, c := range rows {
		released := min(remaining, c.Bytes)
		if err := k.releaseChunk(ctx, contract, c, released); err != nil {
			return 0, err
		}
		remaining -= released
	}
	return n - remaining, k.subBytes(ctx, contract, n-remaining)
}

func (k Keeper) carried(ctx context.Context, contract sdk.AccAddress) (uint64, error) {
	n, err := k.ReleaseCarry.Get(ctx, contract.Bytes())
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to load release carry of %s: %w", contract, err)
	}
	return n, nil
}

func (k Keeper) setCarry(ctx context.Context, contract sdk.AccAddress, n uint64) error {
	if n == 0 {
		return k.ReleaseCarry.Remove(ctx, contract.Bytes())
	}
	return k.ReleaseCarry.Set(ctx, contract.Bytes(), n)
}

func (k Keeper) allCarry(ctx context.Context) ([]types.ReleaseCarry, error) {
	out := []types.ReleaseCarry{}
	err := k.ReleaseCarry.Walk(ctx, nil, func(key []byte, n uint64) (bool, error) {
		out = append(out, types.ReleaseCarry{Contract: sdk.AccAddress(key).String(), Bytes: n})
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk release carry: %w", err)
	}
	return out, nil
}

func (k Keeper) importCarry(ctx context.Context, carry []types.ReleaseCarry) error {
	for _, c := range carry {
		contract, err := sdk.AccAddressFromBech32(c.Contract)
		if err != nil {
			return fmt.Errorf("release carry contract %q: %w", c.Contract, err)
		}
		if err := k.ReleaseCarry.Set(ctx, contract.Bytes(), c.Bytes); err != nil {
			return fmt.Errorf("failed to import release carry of %s: %w", c.Contract, err)
		}
	}
	return nil
}

func (k Keeper) releaseChunk(ctx context.Context, contract sdk.AccAddress, c types.DepositChunk, released uint64) error {
	key := collections.Join(contract.Bytes(), c.Seq)
	if released == c.Bytes {
		if _, _, err := k.fees.ReleaseDeposit(ctx, c.DepositID); err != nil {
			return fmt.Errorf("failed to release deposit %s: %w", c.DepositID, err)
		}
		payer, err := sdk.AccAddressFromBech32(c.Payer)
		if err != nil {
			return fmt.Errorf("deposit chunk %s payer %q: %w", c.DepositID, c.Payer, err)
		}
		if err := k.PayerChunk.Remove(ctx, collections.Join(contract.Bytes(), payer.Bytes())); err != nil {
			return fmt.Errorf("failed to unindex deposit chunk %s: %w", c.DepositID, err)
		}
		if err := k.addLocked(ctx, contract, -1, c.Amount().Neg()); err != nil {
			return err
		}
		return k.Chunks.Remove(ctx, key)
	}
	part := math.NewIntFromUint64(released).Mul(c.PerByte)
	if _, _, err := k.fees.ReleaseDepositPart(ctx, c.DepositID, part); err != nil {
		return fmt.Errorf("failed to release part of deposit %s: %w", c.DepositID, err)
	}
	if err := k.addLocked(ctx, contract, 0, part.Neg()); err != nil {
		return err
	}
	c.Bytes -= released
	return k.Chunks.Set(ctx, key, c)
}

func (k Keeper) contractBytes(ctx context.Context, contract sdk.AccAddress) (uint64, error) {
	n, err := k.ContractBytes.Get(ctx, contract.Bytes())
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to load charged bytes of %s: %w", contract, err)
	}
	return n, nil
}

func (k Keeper) addBytes(ctx context.Context, contract sdk.AccAddress, n uint64) error {
	held, err := k.contractBytes(ctx, contract)
	if err != nil {
		return err
	}
	if err := k.ContractBytes.Set(ctx, contract.Bytes(), held+n); err != nil {
		return fmt.Errorf("failed to store charged bytes of %s: %w", contract, err)
	}
	return k.addGlobal(ctx, 0, int64(n), math.ZeroInt())
}

// subBytes takes n bytes off the contract's charged total, never below zero.
func (k Keeper) subBytes(ctx context.Context, contract sdk.AccAddress, n uint64) error {
	held, err := k.contractBytes(ctx, contract)
	if err != nil {
		return err
	}
	n = min(n, held)
	if n == held {
		err = k.ContractBytes.Remove(ctx, contract.Bytes())
	} else {
		err = k.ContractBytes.Set(ctx, contract.Bytes(), held-n)
	}
	if err != nil {
		return fmt.Errorf("failed to store charged bytes of %s: %w", contract, err)
	}
	return k.addGlobal(ctx, 0, -int64(n), math.ZeroInt())
}

// ChargedBytes returns the bytes of contract that currently carry a deposit.
func (k Keeper) ChargedBytes(ctx context.Context, contract sdk.AccAddress) (uint64, error) {
	return k.contractBytes(ctx, contract)
}

func (k Keeper) allChunks(ctx context.Context) ([]types.DepositChunk, error) {
	out := []types.DepositChunk{}
	err := k.Chunks.Walk(ctx, nil, func(_ collections.Pair[[]byte, uint64], c types.DepositChunk) (bool, error) {
		out = append(out, c)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk deposit chunks: %w", err)
	}
	return out, nil
}

// importChunks restores an exported ledger and its per-contract totals. The x/fees deposits
// behind the chunks come back with x/fees's own genesis.
func (k Keeper) importChunks(ctx context.Context, chunks []types.DepositChunk) error {
	var next uint64
	for _, c := range chunks {
		contract, err := sdk.AccAddressFromBech32(c.Contract)
		if err != nil {
			return fmt.Errorf("deposit chunk contract %q: %w", c.Contract, err)
		}
		if err := k.Chunks.Set(ctx, collections.Join(contract.Bytes(), c.Seq), c); err != nil {
			return fmt.Errorf("failed to import deposit chunk %s: %w", c.DepositID, err)
		}
		payer, err := sdk.AccAddressFromBech32(c.Payer)
		if err != nil {
			return fmt.Errorf("deposit chunk payer %q: %w", c.Payer, err)
		}
		if err := k.PayerChunk.Set(ctx, collections.Join(contract.Bytes(), payer.Bytes()), c.Seq); err != nil {
			return fmt.Errorf("failed to index deposit chunk %s by payer: %w", c.DepositID, err)
		}
		if err := k.addLocked(ctx, contract, 1, c.Amount()); err != nil {
			return err
		}
		if err := k.addBytes(ctx, contract, c.Bytes); err != nil {
			return err
		}
		next = max(next, c.Seq+1)
	}
	return k.NextChunk.Set(ctx, next)
}
