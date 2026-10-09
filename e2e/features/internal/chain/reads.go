//go:build e2e_fleet

package chain

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// EpochState is orama.emission.v1.EpochState.
type EpochState struct {
	CurrentEpoch                Int `json:"current_epoch"`
	EpochStartUnixNano          Int `json:"epoch_start_unix_nano"`
	BlocksInEpoch               Int `json:"blocks_in_epoch"`
	CumulativeMinted            Int `json:"cumulative_minted"`
	CumulativeBurned            Int `json:"cumulative_burned"`
	GenesisSupply               Int `json:"genesis_supply"`
	CumulativeDevelopmentMinted Int `json:"cumulative_development_minted"`
	CumulativeServiceMinted     Int `json:"cumulative_service_minted"`
	CumulativeFaucetMinted      Int `json:"cumulative_faucet_minted"`
}

// ExpectedSupply is the bank supply the epoch state accounts for (docs/CHAIN.md "Invariants"):
// genesis supply plus every mint (the epoch, development, service and test-network faucet ones)
// minus the burns.
func (e EpochState) ExpectedSupply() Int {
	return e.GenesisSupply.Add(e.CumulativeMinted).Add(e.CumulativeDevelopmentMinted).
		Add(e.CumulativeServiceMinted).Add(e.CumulativeFaucetMinted).Sub(e.CumulativeBurned)
}

// Epoch reads x/emission's live epoch state, at height when height > 0.
func (c *Chain) Epoch(t testing.TB, n fleet.Node, height int64) EpochState {
	t.Helper()
	var r struct {
		EpochState EpochState `json:"epoch_state"`
	}
	if height > 0 {
		c.QueryAt(t, n, height, &r, "emission", "current-epoch")
	} else {
		c.Query(t, n, &r, "emission", "current-epoch")
	}
	return r.EpochState
}

// FeeCounters are x/fees' cumulative settlement counters (its Invariants
// response): every settled fee adds to collected, and splits into burned
// (the base fee) and distributed (the tip).
type FeeCounters struct {
	Collected   Int `json:"cumulative_collected"`
	Burned      Int `json:"cumulative_burned"`
	Distributed Int `json:"cumulative_distributed"`
}

// Fees reads the fee counters at height (0: latest).
func (c *Chain) Fees(t testing.TB, n fleet.Node, height int64) FeeCounters {
	t.Helper()
	var r FeeCounters
	if height > 0 {
		c.QueryAt(t, n, height, &r, "fees", "invariants")
	} else {
		c.Query(t, n, &r, "fees", "invariants")
	}
	return r
}

// EarningsAt is addr's earnings at a past height.
func (c *Chain) EarningsAt(t testing.TB, n fleet.Node, addr string, height int64) Int {
	t.Helper()
	var r struct {
		Balance Int `json:"balance"`
	}
	c.QueryAt(t, n, height, &r, "fees", "earnings", addr)
	return r.Balance
}

// BankAt is addr's norama bank balance at a past height.
func (c *Chain) BankAt(t testing.TB, n fleet.Node, addr string, height int64) Int {
	t.Helper()
	var r struct {
		Balance struct {
			Amount Int `json:"amount"`
		} `json:"balance"`
	}
	c.QueryAt(t, n, height, &r, "bank", "balance", addr, Denom)
	return r.Balance.Amount
}

// BaseFeeAt is the base fee in force after block height.
func (c *Chain) BaseFeeAt(t testing.TB, n fleet.Node, height int64) Int {
	t.Helper()
	var r struct {
		BaseFee Int `json:"base_fee"`
	}
	c.QueryAt(t, n, height, &r, "fees", "base-fee")
	return r.BaseFee
}
