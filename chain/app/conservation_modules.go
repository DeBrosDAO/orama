package app

import (
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// moduleLedgerChecks wraps each module's CheckInvariants, the checks that until now were reachable
// only as gRPC queries, as checks of the full pass. Each compares a module's ledger with the module
// account that holds its coins (or, for x/wasmpolicy and x/storage, with the rows it derives from).
// x/shielded runs without its nullifier-database fold, which reads a database outside the committed
// state (a node-local fault must not be a consensus failure) and is checked when a node starts.
func (app *OramaApp) moduleLedgerChecks() []conservationCheck {
	return []conservationCheck{
		{"x/token", app.checkTokenLedgers},
		{"x/power", app.checkPowerLedgers},
		{"x/fees", app.checkFeesLedgers},
		{"x/nodes", app.checkNodesLedgers},
		{"x/shielded", app.checkShieldedLedgers},
		{"x/houses", app.checkHousesLedgers},
		{"x/storage", app.checkStorageLedgers},
		{"x/market", app.checkMarketLedgers},
		{"x/relay", app.checkRelayLedgers},
		{"x/wasmpolicy", app.checkWasmPolicyLedgers},
	}
}

// ledgerVerdict turns a module's named boolean checks into one error naming the broken ones.
func ledgerVerdict(detail string, checks map[string]bool, order ...string) error {
	var broken []string
	for _, name := range order {
		if !checks[name] {
			broken = append(broken, name)
		}
	}
	if len(broken) == 0 {
		return nil
	}
	return fmt.Errorf("%s broken: %s", strings.Join(broken, ", "), strings.TrimSpace(detail))
}

func (app *OramaApp) checkTokenLedgers(ctx sdk.Context) error {
	inv, err := app.TokenKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{"issued equals bank supply": inv.SupplyMatches, "metadata deposits": inv.DepositsMatch},
		"issued equals bank supply", "metadata deposits")
}

func (app *OramaApp) checkPowerLedgers(ctx sdk.Context) error {
	detail, empty := app.PowerKeeper.CheckInvariants(ctx)
	return ledgerVerdict(detail, map[string]bool{"module account empty": empty}, "module account empty")
}

func (app *OramaApp) checkFeesLedgers(ctx sdk.Context) error {
	inv, err := app.FeesKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{
		"earnings match module": inv.EarningsMatchModule, "deposits match module": inv.DepositsMatchModule, "collected equals burned plus distributed": inv.FeesBalance,
	}, "earnings match module", "deposits match module", "collected equals burned plus distributed")
}

func (app *OramaApp) checkNodesLedgers(ctx sdk.Context) error {
	inv, err := app.NodesKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{
		"balance matches": inv.BalanceMatches, "active roles bonded": inv.ActiveRolesBonded, "capacity backed": inv.CapacityBacked,
	}, "balance matches", "active roles bonded", "capacity backed")
}

func (app *OramaApp) checkShieldedLedgers(ctx sdk.Context) error {
	inv, err := app.ShieldedKeeper.CheckLedgers(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{
		"module balance matches pools": inv.BalanceMatches, "pools non-negative": inv.PoolsNonNegative,
		"subtree roots match the tree": inv.SubtreeRootsMatch, "queue owner index matches": inv.QueueIndexMatches,
	}, "module balance matches pools", "pools non-negative", "subtree roots match the tree", "queue owner index matches")
}

func (app *OramaApp) checkHousesLedgers(ctx sdk.Context) error {
	inv, err := app.HousesKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{"bonds match module": inv.BondsMatchModule}, "bonds match module")
}

func (app *OramaApp) checkStorageLedgers(ctx sdk.Context) error {
	inv, err := app.StorageKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{
		"escrow conserved": inv.EscrowConserved, "subsidy within ceiling": inv.SubsidyWithinCeiling, "distinct operators": inv.DistinctOperators,
		"reserved within declared": inv.ReservedWithinDeclared, "queue well formed": inv.QueueWellFormed,
	}, "escrow conserved", "subsidy within ceiling", "distinct operators", "reserved within declared", "queue well formed")
}

func (app *OramaApp) checkMarketLedgers(ctx sdk.Context) error {
	detail, broken, err := app.MarketKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(detail, map[string]bool{"bid escrow matches module": !broken}, "bid escrow matches module")
}

func (app *OramaApp) checkRelayLedgers(ctx sdk.Context) error {
	inv, err := app.RelayKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{"ceiling holds": inv.CeilingHolds, "payouts match": inv.PayoutsMatch}, "ceiling holds", "payouts match")
}

func (app *OramaApp) checkWasmPolicyLedgers(ctx sdk.Context) error {
	inv, err := app.WasmPolicyKeeper.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	return ledgerVerdict(inv.Detail, map[string]bool{"bytes match": inv.BytesMatch, "deposits match": inv.DepositsMatch}, "bytes match", "deposits match")
}
