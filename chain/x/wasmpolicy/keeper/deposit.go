package keeper

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	ReleaseDepositPart(ctx context.Context, id string, part math.Int) (refund, burn math.Int, err error)
	GetDeposit(ctx context.Context, id string) (feestypes.Deposit, error)
}

// ApplyStateDelta settles one contract call's net change of stored bytes.
//
// Growth locks growth * deposit_per_byte from payer as a new chunk. A shrink releases chunks
// newest first, so the bytes that were added last are refunded first, each to the payer who locked
// it. Only bytes that were charged can be refunded: a contract's first bytes (for example state
// present at genesis) were never charged, so a shrink past the charged total releases the
// charged total and no more.
func (k Keeper) ApplyStateDelta(ctx context.Context, contract, payer sdk.AccAddress, grew, shrank uint64) error {
	switch {
	case grew > shrank:
		return k.charge(ctx, contract, payer, grew-shrank)
	case shrank > grew:
		return k.refund(ctx, contract, shrank-grew)
	default:
		return nil
	}
}

func (k Keeper) charge(ctx context.Context, contract, payer sdk.AccAddress, n uint64) error {
	if payer.Empty() {
		return fmt.Errorf("%w: contract %s grew by %d bytes", types.ErrDepositPayer, contract, n)
	}
	perByte, err := k.DepositPerByte.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load deposit_per_byte: %w", err)
	}
	seq, err := k.NextChunk.Next(ctx)
	if err != nil {
		return fmt.Errorf("failed to take a deposit chunk sequence: %w", err)
	}
	chunk := types.DepositChunk{
		Contract: contract.String(), Seq: seq, Payer: payer.String(), Bytes: n, PerByte: perByte,
		DepositID: types.DepositID(contract.String(), seq),
	}
	if err := k.fees.LockDeposit(ctx, payer, chunk.DepositID, chunk.Amount()); err != nil {
		return fmt.Errorf("state deposit for %d new bytes of contract %s: %w", n, contract, err)
	}
	if err := k.Chunks.Set(ctx, collections.Join(contract.Bytes(), seq), chunk); err != nil {
		return fmt.Errorf("failed to record deposit chunk %s: %w", chunk.DepositID, err)
	}
	return k.addBytes(ctx, contract, n)
}

func (k Keeper) refund(ctx context.Context, contract sdk.AccAddress, n uint64) error {
	held, err := k.contractBytes(ctx, contract)
	if err != nil {
		return err
	}
	if n > held {
		n = held
	}
	rng := collections.NewPrefixedPairRange[[]byte, uint64](contract.Bytes()).Descending()
	var rows []types.DepositChunk
	remaining := n
	err = k.Chunks.Walk(ctx, rng, func(_ collections.Pair[[]byte, uint64], c types.DepositChunk) (bool, error) {
		rows = append(rows, c)
		if c.Bytes >= remaining {
			return true, nil
		}
		remaining -= c.Bytes
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk deposit chunks of %s: %w", contract, err)
	}
	remaining = n
	for _, c := range rows {
		released := min(remaining, c.Bytes)
		if err := k.releaseChunk(ctx, contract, c, released); err != nil {
			return err
		}
		remaining -= released
	}
	return k.subBytes(ctx, contract, n)
}

func (k Keeper) releaseChunk(ctx context.Context, contract sdk.AccAddress, c types.DepositChunk, released uint64) error {
	key := collections.Join(contract.Bytes(), c.Seq)
	if released == c.Bytes {
		if _, _, err := k.fees.ReleaseDeposit(ctx, c.DepositID); err != nil {
			return fmt.Errorf("failed to release deposit %s: %w", c.DepositID, err)
		}
		return k.Chunks.Remove(ctx, key)
	}
	part := math.NewIntFromUint64(released).Mul(c.PerByte)
	if _, _, err := k.fees.ReleaseDepositPart(ctx, c.DepositID, part); err != nil {
		return fmt.Errorf("failed to release part of deposit %s: %w", c.DepositID, err)
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
	return k.ContractBytes.Set(ctx, contract.Bytes(), held+n)
}

func (k Keeper) subBytes(ctx context.Context, contract sdk.AccAddress, n uint64) error {
	held, err := k.contractBytes(ctx, contract)
	if err != nil {
		return err
	}
	if n >= held {
		return k.ContractBytes.Remove(ctx, contract.Bytes())
	}
	return k.ContractBytes.Set(ctx, contract.Bytes(), held-n)
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
		if err := k.addBytes(ctx, contract, c.Bytes); err != nil {
			return err
		}
		next = max(next, c.Seq+1)
	}
	return k.NextChunk.Set(ctx, next)
}

// Invariants is the result of CheckInvariants.
type Invariants struct {
	BytesMatch    bool
	DepositsMatch bool
	Detail        string
}

// CheckInvariants checks, for every contract with a deposit, that
//
//   - the charged byte total equals the sum of its chunks, and
//   - every chunk's x/fees deposit exists, belongs to the chunk's payer, and holds exactly
//     bytes * per_byte.
//
// The walk is O(chunks). The x/fees invariant (deposit module balance equals open deposits)
// covers these deposits too, since they are ordinary x/fees deposits.
func (k Keeper) CheckInvariants(ctx context.Context) (Invariants, error) {
	inv := Invariants{BytesMatch: true, DepositsMatch: true}
	sums := map[string]uint64{}
	chunks, err := k.allChunks(ctx)
	if err != nil {
		return inv, err
	}
	for _, c := range chunks {
		sums[c.Contract] += c.Bytes
		dep, err := k.fees.GetDeposit(ctx, c.DepositID)
		switch {
		case err != nil:
			inv.DepositsMatch = false
			inv.Detail += fmt.Sprintf("chunk %s has no x/fees deposit: %v\n", c.DepositID, err)
		case dep.Owner != c.Payer || !dep.Amount.Equal(c.Amount()):
			inv.DepositsMatch = false
			inv.Detail += fmt.Sprintf("chunk %s: deposit is %s of %s, chunk says %s of %s\n", c.DepositID, dep.Amount, dep.Owner, c.Amount(), c.Payer)
		}
	}
	err = k.ContractBytes.Walk(ctx, nil, func(key []byte, held uint64) (bool, error) {
		addr := sdk.AccAddress(key).String()
		if sums[addr] != held {
			inv.BytesMatch = false
			inv.Detail += fmt.Sprintf("contract %s: charged bytes %d, chunks sum to %d\n", addr, held, sums[addr])
		}
		delete(sums, addr)
		return false, nil
	})
	if err != nil {
		return inv, fmt.Errorf("failed to walk charged bytes: %w", err)
	}
	orphans := make([]string, 0, len(sums))
	for addr := range sums {
		orphans = append(orphans, addr)
	}
	sort.Strings(orphans)
	for _, addr := range orphans {
		inv.BytesMatch = false
		inv.Detail += fmt.Sprintf("contract %s: %d bytes in chunks and no charged-bytes row\n", addr, sums[addr])
	}
	return inv, nil
}
