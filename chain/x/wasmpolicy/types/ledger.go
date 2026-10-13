package types

import "cosmossdk.io/math"

// ContractTotals is the running total of one contract's deposit chunks: how many rows it has and
// how many norama they hold in x/fees. It is kept by every write to the ledger, never recomputed
// from the chunks, and derived again from the chunks when a genesis is imported.
type ContractTotals struct {
	Rows   uint64   `json:"rows"`
	Locked math.Int `json:"locked"`
}

// Totals is the running total over every contract: the sum of the per-contract totals plus the
// charged bytes. The epoch invariant compares it with the sums of the per-contract rows.
type Totals struct {
	Rows   uint64   `json:"rows"`
	Bytes  uint64   `json:"bytes"`
	Locked math.Int `json:"locked"`
}
