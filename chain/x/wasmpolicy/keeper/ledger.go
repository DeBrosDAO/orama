package keeper

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func (k Keeper) globalTotals(ctx context.Context) (types.Totals, error) {
	t, err := k.Totals.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return types.Totals{Locked: math.ZeroInt()}, nil
	}
	if err != nil {
		return types.Totals{}, fmt.Errorf("failed to load the deposit totals: %w", err)
	}
	return t, nil
}

// addGlobal adds the signed deltas to the total over every contract.
func (k Keeper) addGlobal(ctx context.Context, rows, bytes int64, locked math.Int) error {
	t, err := k.globalTotals(ctx)
	if err != nil {
		return err
	}
	t.Rows = uint64(int64(t.Rows) + rows)
	t.Bytes = uint64(int64(t.Bytes) + bytes)
	t.Locked = t.Locked.Add(locked)
	if err := k.Totals.Set(ctx, t); err != nil {
		return fmt.Errorf("failed to store the deposit totals: %w", err)
	}
	return nil
}

// addLocked adds the signed deltas to a contract's rows and locked norama, and to the global total.
// A contract left with no rows has no totals row.
func (k Keeper) addLocked(ctx context.Context, contract sdk.AccAddress, rows int64, locked math.Int) error {
	t, err := k.ContractTotals.Get(ctx, contract.Bytes())
	if errors.Is(err, collections.ErrNotFound) {
		t = types.ContractTotals{Locked: math.ZeroInt()}
	} else if err != nil {
		return fmt.Errorf("failed to load the deposit totals of %s: %w", contract, err)
	}
	t.Rows = uint64(int64(t.Rows) + rows)
	t.Locked = t.Locked.Add(locked)
	if t.Rows == 0 {
		err = k.ContractTotals.Remove(ctx, contract.Bytes())
	} else {
		err = k.ContractTotals.Set(ctx, contract.Bytes(), t)
	}
	if err != nil {
		return fmt.Errorf("failed to store the deposit totals of %s: %w", contract, err)
	}
	return k.addGlobal(ctx, rows, 0, locked)
}

// Invariants is the result of CheckInvariants and CheckInvariantsExact.
type Invariants struct {
	BytesMatch    bool
	DepositsMatch bool
	Detail        string
}

// CheckInvariants is the check the chain runs every epoch. It reads the running totals only, one
// row per contract and never a chunk, so its cost does not grow with the number of payers. It checks
//
//   - the charged bytes of all contracts sum to the global total, and every contract with bytes has
//     rows and every contract with rows has bytes, and
//   - the rows and locked norama of all contracts sum to the global totals.
//
// The totals are written by the same code that writes the chunks, so a bug that drifts one from the
// other shows here; CheckInvariantsExact compares them with the chunks themselves. It costs
// O(chunks), so the chain runs it once at the end of InitGenesis and once in ExportGenesis, never
// in a block.
func (k Keeper) CheckInvariants(ctx context.Context) (Invariants, error) {
	inv := Invariants{BytesMatch: true, DepositsMatch: true}
	global, err := k.globalTotals(ctx)
	if err != nil {
		return inv, err
	}
	var bytes, rows uint64
	locked := math.ZeroInt()
	err = k.ContractBytes.Walk(ctx, nil, func(key []byte, held uint64) (bool, error) {
		bytes += held
		has, err := k.ContractTotals.Has(ctx, key)
		if !has && err == nil {
			inv.BytesMatch = false
			inv.Detail += fmt.Sprintf("contract %s has %d charged bytes and no deposit rows\n", sdk.AccAddress(key), held)
		}
		return false, err
	})
	if err != nil {
		return inv, fmt.Errorf("failed to walk charged bytes: %w", err)
	}
	err = k.ContractTotals.Walk(ctx, nil, func(key []byte, t types.ContractTotals) (bool, error) {
		rows += t.Rows
		locked = locked.Add(t.Locked)
		held, err := k.contractBytes(ctx, sdk.AccAddress(key))
		if err != nil {
			return true, err
		}
		if held == 0 {
			inv.BytesMatch = false
			inv.Detail += fmt.Sprintf("contract %s has %d deposit rows and no charged bytes\n", sdk.AccAddress(key), t.Rows)
		}
		return false, nil
	})
	if err != nil {
		return inv, fmt.Errorf("failed to walk the deposit totals: %w", err)
	}
	if bytes != global.Bytes {
		inv.BytesMatch = false
		inv.Detail += fmt.Sprintf("contracts hold %d charged bytes, the global total says %d\n", bytes, global.Bytes)
	}
	if rows != global.Rows || !locked.Equal(global.Locked) {
		inv.DepositsMatch = false
		inv.Detail += fmt.Sprintf("contracts have %d rows locking %s, the global total says %d rows and %s\n", rows, locked, global.Rows, global.Locked)
	}
	return inv, nil
}

// CheckInvariantsExact checks, for every contract with a deposit, that
//
//   - the charged byte total equals the sum of its chunks,
//   - its running rows and locked norama equal the chunks',
//   - the (contract, payer) index has exactly its chunks, each under its own payer, and
//   - every chunk's x/fees deposit exists, belongs to the chunk's payer, and holds exactly
//     (bytes + overhead) * per_byte.
//
// The walk is O(chunks): InitGenesis and ExportGenesis run it once each and refuse a ledger it
// finds broken (checkLedgerExact). The x/fees invariant (deposit module balance equals open deposits)
// covers these deposits too, since they are ordinary x/fees deposits.
func (k Keeper) CheckInvariantsExact(ctx context.Context) (Invariants, error) {
	inv := Invariants{BytesMatch: true, DepositsMatch: true}
	chunks, err := k.allChunks(ctx)
	if err != nil {
		return inv, err
	}
	sums := map[string]uint64{}
	rows := map[string]uint64{}
	locked := map[string]math.Int{}
	for _, c := range chunks {
		sums[c.Contract] += c.Bytes
		rows[c.Contract]++
		if _, ok := locked[c.Contract]; !ok {
			locked[c.Contract] = math.ZeroInt()
		}
		locked[c.Contract] = locked[c.Contract].Add(c.Amount())
		inv.checkChunk(ctx, k, c)
	}
	if err := k.checkPayerIndex(ctx, &inv, chunks); err != nil {
		return inv, err
	}
	err = k.ContractBytes.Walk(ctx, nil, func(key []byte, held uint64) (bool, error) {
		addr := sdk.AccAddress(key).String()
		if sums[addr] != held {
			inv.BytesMatch = false
			inv.Detail += fmt.Sprintf("contract %s: charged bytes %d, chunks sum to %d\n", addr, held, sums[addr])
		}
		if err := inv.checkTotals(ctx, k, key, rows[addr], locked[addr]); err != nil {
			return true, err
		}
		delete(sums, addr)
		return false, nil
	})
	if err != nil {
		return inv, fmt.Errorf("failed to walk charged bytes: %w", err)
	}
	orphans := make([]string, 0, len(sums))
	// nosemgrep: consensus-map-iteration -- keys are collected and sorted before use
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

// checkLedgerExact runs CheckInvariantsExact and turns a broken ledger into an error, so a genesis
// that imports or exports a ledger that disagrees with its deposits stops the chain.
func (k Keeper) checkLedgerExact(ctx context.Context) error {
	inv, err := k.CheckInvariantsExact(ctx)
	if err != nil {
		return fmt.Errorf("failed to check the deposit ledger: %w", err)
	}
	if !inv.BytesMatch || !inv.DepositsMatch {
		return fmt.Errorf("the deposit ledger is inconsistent: %s", inv.Detail)
	}
	return nil
}

// checkChunk compares one chunk with its x/fees deposit.
func (inv *Invariants) checkChunk(ctx context.Context, k Keeper, c types.DepositChunk) {
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

// checkTotals compares a contract's running rows and locked norama with what its chunks sum to.
func (inv *Invariants) checkTotals(ctx context.Context, k Keeper, key []byte, rows uint64, locked math.Int) error {
	if locked.IsNil() {
		locked = math.ZeroInt()
	}
	t, err := k.ContractTotals.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		t = types.ContractTotals{Locked: math.ZeroInt()}
	} else if err != nil {
		return fmt.Errorf("failed to load the deposit totals of %s: %w", sdk.AccAddress(key), err)
	}
	if t.Rows != rows || !t.Locked.Equal(locked) {
		inv.DepositsMatch = false
		inv.Detail += fmt.Sprintf("contract %s: running totals say %d rows locking %s, chunks have %d rows locking %s\n", sdk.AccAddress(key), t.Rows, t.Locked, rows, locked)
	}
	return nil
}

// checkPayerIndex checks that the (contract, payer) index holds exactly one entry per chunk.
func (k Keeper) checkPayerIndex(ctx context.Context, inv *Invariants, chunks []types.DepositChunk) error {
	for _, c := range chunks {
		contract, err := sdk.AccAddressFromBech32(c.Contract)
		if err != nil {
			return fmt.Errorf("deposit chunk contract %q: %w", c.Contract, err)
		}
		payer, err := sdk.AccAddressFromBech32(c.Payer)
		if err != nil {
			return fmt.Errorf("deposit chunk payer %q: %w", c.Payer, err)
		}
		seq, err := k.PayerChunk.Get(ctx, collections.Join(contract.Bytes(), payer.Bytes()))
		if err != nil || seq != c.Seq {
			inv.DepositsMatch = false
			inv.Detail += fmt.Sprintf("chunk %s is not under its payer in the index (found %d, %v)\n", c.DepositID, seq, err)
		}
	}
	var indexed int
	err := k.PayerChunk.Walk(ctx, nil, func(collections.Pair[[]byte, []byte], uint64) (bool, error) {
		indexed++
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk the payer index: %w", err)
	}
	if indexed != len(chunks) {
		inv.DepositsMatch = false
		inv.Detail += fmt.Sprintf("the payer index has %d entries for %d chunks\n", indexed, len(chunks))
	}
	return nil
}
