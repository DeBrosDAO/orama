package indexer

import (
	"cosmossdk.io/math"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// supplyPoint builds the supply breakdown after an epoch's closing block. The total supply and each
// module account's balance are bank queries at the closing height; minted and burned to date are
// x/emission's running totals. Bonded and unbonding are the two staking pools' balances, the
// earnings pools are x/fees' account (whose balance is the sum of every earnings ledger entry) and
// the storage escrow is x/storage's escrow account. Whatever the other module accounts hold is
// OtherProtocol, and Circulating is the supply none of them hold.
func supplyPoint(r chainReader, epoch uint64, b blockFacts, state emissiontypes.EpochState, cur cumulatives) (SupplyPoint, error) {
	total, err := r.totalSupply()
	if err != nil {
		return SupplyPoint{}, err
	}
	held := math.ZeroInt()
	named := map[string]math.Int{}
	for _, name := range protocolModules {
		addr, err := moduleAddress(name)
		if err != nil {
			return SupplyPoint{}, err
		}
		bal, err := r.balance(addr)
		if err != nil {
			return SupplyPoint{}, err
		}
		named[name] = bal
		held = held.Add(bal)
	}
	bonded, unbonding := named[stakingtypes.BondedPoolName], named[stakingtypes.NotBondedPoolName]
	earnings, escrow := named[feestypes.ModuleName], named[storagetypes.EscrowModuleName]
	other := held.Sub(bonded).Sub(unbonding).Sub(earnings).Sub(escrow)
	return SupplyPoint{
		Epoch: epoch, Height: b.height, Time: b.time.UTC(),
		TotalSupply:  total.String(),
		MintedToDate: cur.Minted.Add(cur.Development).Add(cur.Service).Add(cur.Faucet).String(),
		BurnedToDate: cur.Burned.String(), GenesisSupply: state.GenesisSupply.String(),
		Bonded: bonded.String(), Unbonding: unbonding.String(),
		EarningsPools: earnings.String(), StorageEscrow: escrow.String(),
		OtherProtocol: other.String(), Circulating: total.Sub(held).String(),
	}, nil
}
