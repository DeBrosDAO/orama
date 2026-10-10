package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// TestApp_aStorageProviderIsPaidForEveryEpochItProvesACustomersDeal drives a customer-paid PUBLIC_PIN
// deal through the real app: three bonded providers accept the three replicas, answer the epoch's
// challenge every epoch, and the chain pays each operator's earnings 90% of the price per proved
// replica-epoch, burns 5% and sends 5% to the archive fund. There is no
// subsidy (a PUBLIC_PIN deal has none, and a PRIVATE one needs 8 operators), so the payout is the
// customer's escrow and nothing else.
func TestApp_aStorageProviderIsPaidForEveryEpochItProvesACustomersDeal(t *testing.T) {
	const price = 1000
	c := newWiringChain(t)
	nodes := map[string]wiringNode{}
	for _, n := range []wiringNode{
		c.addStorageNode("s1", "https://45.33.100.10:443", 15169),
		c.addStorageNode("s2", "https://93.184.113.10:443", 13335),
		c.addStorageNode("s3", "https://151.101.2.10:443", 16509),
	} {
		nodes[n.id] = n
	}
	c.blocks(1)

	data := protocolPayload(7)
	client := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	var dealID uint64
	c.write(func(ctx sdk.Context) {
		funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(params.NoramaPerOrama).MulRaw(10)))
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, funds))
		require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, client, funds))
		commitment, err := piece.Commit(data)
		require.NoError(t, err)
		nonce := make([]byte, storagetypes.NonceLen)
		nonce[0] = 3
		res, err := storagekeeper.NewMsgServer(c.app.StorageKeeper).CreateDeal(ctx, &storagetypes.MsgCreateDeal{
			Signer: client.String(), Class: storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN, DealNonce: nonce,
			Replicas: 3, PricePerEpoch: math.NewInt(price), DurationEpochs: 100,
			Pieces: []storagetypes.PieceCommitment{{
				Root: commitment.Root, RealLeafCount: commitment.RealLeafCount, PaddedLeafCount: commitment.PaddedLeafCount, PieceBytes: uint64(len(data)),
			}},
		})
		require.NoError(t, err)
		dealID = res.DealId
	})
	c.blocks(1)
	c.acceptSlots(dealID, nodes)

	const rounds = 4
	for round := 0; round < rounds; round++ {
		c.blocks(1) // opens the epoch's challenges; the block before closed and settled the last epoch.
		c.proveEverySlot(dealID, data, nodes)
	}
	c.blocks(2) // the last epoch closes and its settlements drain

	ctx := c.app.NewContext(true)
	total := math.ZeroInt()
	for id, n := range nodes {
		earned := c.earningsOf(n.operator)
		total = total.Add(earned)
		require.True(t, earned.IsPositive(), "%s was paid for the epochs it proved", id)
		require.True(t, earned.ModRaw(price*90/100).IsZero(), "%s earned %s, not a whole number of 900-norama payments (90% of %d)", id, earned, price)
		require.True(t, earned.GTE(math.NewInt(2*price*90/100)), "%s earned %s, less than two proved epochs", id, earned)
	}
	fund, err := c.app.StorageKeeper.ArchiveFund.Get(ctx)
	require.NoError(t, err)
	require.True(t, fund.MulRaw(18).Equal(total), "the archive fund %s is 5% of what was paid: operators earned %s (90%%)", fund, total)
	c.requireModuleInvariants()
	feesInv, err := c.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}
