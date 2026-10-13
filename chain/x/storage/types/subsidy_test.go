package types_test

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestServiceSplit(t *testing.T) {
	to, burn, archive := types.SplitServicePayment(math.NewInt(1000))
	require.Equal(t, math.NewInt(900), to)
	require.Equal(t, math.NewInt(50), burn)
	require.Equal(t, math.NewInt(50), archive)
	require.Equal(t, math.NewInt(1000), to.Add(burn).Add(archive))
}

func TestSMaxIsHardCoded(t *testing.T) {
	require.True(t, types.SMax.Equal(math.LegacyOneDec()))
	require.True(t, types.SubsidyRate(1_000_000, 1, 1).Equal(types.SMax))
	require.True(t, types.SubsidyRate(7, 8, 16).IsZero())
	// (8-8+1)/(16-8+1) = 1/9, then up to 1 and never past it.
	require.True(t, types.SubsidyRate(8, 8, 16).Equal(math.LegacyNewDec(1).Quo(math.LegacyNewDec(9))))
	require.True(t, types.SubsidyRate(16, 8, 16).Equal(math.LegacyOneDec()))
}

func TestEpochExtensionDoesNotMultiplySubsidy(t *testing.T) {
	price := math.NewInt(3000)
	rate := math.LegacyMustNewDecFromStr("0.5")
	require.Equal(t, types.ApplyRate(price, rate), types.EpochSubsidyDemand(price, rate, 10))
}

func TestAssignmentSeed(t *testing.T) {
	hash := []byte{1, 2, 3, 4}
	got := types.AssignmentSeed(hash, 7, 2)
	h := sha256.New()
	h.Write(hash)
	var deal [8]byte
	binary.BigEndian.PutUint64(deal[:], 7)
	h.Write(deal[:])
	var j [4]byte
	binary.BigEndian.PutUint32(j[:], 2)
	h.Write(j[:])
	require.Equal(t, h.Sum(nil), got)
}

func TestPickSkipsRepairDelegate(t *testing.T) {
	cands := []types.Candidate{
		{ID: "a", Operator: "op-a", Network16: "n1", ASN: 1, Active: true, Declared: 100, Reserved: 0},
		{ID: "b", Operator: "op-b", Network16: "n2", ASN: 2, Active: true, Declared: 100, Reserved: 0},
		{ID: "c", Operator: "op-c", Network16: "n3", ASN: 3, Active: true, Declared: 100, Reserved: 0},
	}
	rules := types.PickRules{
		PieceBytes: 10, RepairOperator: "op-a",
		UsedOperators: map[string]struct{}{}, UsedNetworks: map[string]struct{}{}, UsedASNs: map[uint32]struct{}{},
	}
	for i := 0; i < 20; i++ {
		got, ok := types.PickCandidate(cands, []byte{byte(i)}, rules)
		require.True(t, ok)
		require.NotEqual(t, "op-a", got.Operator)
	}
}

func TestProtoRoundTrip(t *testing.T) {
	p := types.DefaultParams()
	bz, err := p.Marshal()
	require.NoError(t, err)
	var out types.Params
	require.NoError(t, out.Unmarshal(bz))
	require.True(t, p.DealFee.Equal(out.DealFee))
	require.Equal(t, p.KC, out.KC)
	require.Equal(t, p.SMinProviders, out.SMinProviders)
	require.True(t, p.SlashFraction.Equal(out.SlashFraction))
	require.True(t, p.ProbationDeposit.Equal(out.ProbationDeposit))

	msg := types.MsgCreateDeal{
		Signer: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0dead",
		Class:  types.DealClass_DEAL_CLASS_PRIVATE, DealNonce: make([]byte, 32),
		Replicas: 3, PricePerEpoch: math.NewInt(5), DurationEpochs: 2,
		Pieces: []types.PieceCommitment{{Root: make([]byte, 32), RealLeafCount: 1, PaddedLeafCount: 1, PieceBytes: 1024}},
	}
	raw, err := msg.Marshal()
	require.NoError(t, err)
	var back types.MsgCreateDeal
	require.NoError(t, back.Unmarshal(raw))
	require.Equal(t, msg.Replicas, back.Replicas)
	require.True(t, msg.PricePerEpoch.Equal(back.PricePerEpoch))
	require.Equal(t, msg.Pieces[0].PieceBytes, back.Pieces[0].PieceBytes)
}

func TestSelfDealingSimulation(t *testing.T) {
	const (
		sMin  = uint64(8)
		sFull = uint64(16)
	)
	rate := types.SubsidyRate(10, sMin, sFull)
	require.True(t, rate.Equal(math.LegacyNewDec(1).Quo(math.LegacyNewDec(3))))

	ceiling := math.NewInt(8_000_000)
	cap := types.OperatorSubsidyCap(ceiling, sMin)
	require.Equal(t, "1000000", cap.String())

	// Total rated demand is far above the ceiling. The attacker is one operator
	// holding fraction f of that demand; nine honest operators share the rest.
	totalWant := math.NewInt(30_000_000)
	for _, frac := range []string{"0.1", "0.5", "0.9"} {
		f := math.LegacyMustNewDecFromStr(frac)
		attackerWant := totalWant.ToLegacyDec().Mul(f).TruncateInt()
		honestWant := totalWant.Sub(attackerWant)
		for _, strategy := range []string{"always", "tiny", "extend"} {
			claims := selfDealClaims(strategy, attackerWant, honestWant, rate)
			mints, _ := types.ComputeMintPlan(claims, ceiling, cap)
			attacker, total := sumOperator(claims, mints, "attacker")
			require.Falsef(t, attacker.GT(cap), "f=%s strategy=%s attacker %s exceeds cap %s", frac, strategy, attacker, cap)
			if frac == "0.9" {
				require.Truef(t, attacker.LT(total), "f=0.9 strategy=%s attacker %s took every subsidy %s", frac, attacker, total)
				bare, _ := types.ComputeMintPlan(claims, ceiling, ceiling)
				bareAttacker, _ := sumOperator(claims, bare, "attacker")
				require.Truef(t, bareAttacker.GT(cap), "uncapped f=0.9 attacker %s should exceed the cap; the bound is not trivial", bareAttacker)
				require.Truef(t, bareAttacker.GT(attacker), "the cap must cut the 0.9 attacker below the uncapped mint")
			}
		}
	}

	// Below s_min the ramp is zero, so a 0.9 attacker mints nothing.
	idle := selfDealClaims("always", math.ZeroInt(), math.ZeroInt(), math.LegacyZeroDec())
	mints, _ := types.ComputeMintPlan(idle, ceiling, cap)
	for _, m := range mints {
		require.True(t, m.IsZero())
	}
}

func selfDealClaims(strategy string, attackerWant, honestWant math.Int, rate math.LegacyDec) []types.MintClaim {
	// rate is already applied by the caller for "always" and "tiny". "extend"
	// rebuilds the attacker want through EpochSubsidyDemand to show a longer
	// deal does not scale this epoch.
	if strategy == "extend" {
		// attackerWant = price * rate, so price = attackerWant / rate when rate is 1/3.
		// Passing the already-rated amount through EpochSubsidyDemand with rate 1
		// keeps it, and a non-zero extension must not change it.
		attackerWant = types.EpochSubsidyDemand(attackerWant, math.LegacyOneDec(), 10)
	}
	var claims []types.MintClaim
	switch strategy {
	case "tiny":
		const parts = 10
		each := attackerWant.QuoRaw(parts)
		for i := 0; i < parts; i++ {
			amt := each
			if i == 0 {
				amt = attackerWant.Sub(each.MulRaw(parts - 1))
			}
			claims = append(claims, types.MintClaim{Operator: "attacker", Kind: types.MintSubsidy, Amount: amt, FullPrice: amt})
		}
	default:
		claims = append(claims, types.MintClaim{Operator: "attacker", Kind: types.MintSubsidy, Amount: attackerWant, FullPrice: attackerWant})
	}
	base := honestWant.QuoRaw(9)
	for i := 0; i < 9; i++ {
		amt := base
		if i == 0 {
			amt = honestWant.Sub(base.MulRaw(8))
		}
		claims = append(claims, types.MintClaim{
			Operator: "honest-" + string(rune('a'+i)), Kind: types.MintSubsidy, Amount: amt, FullPrice: amt,
		})
	}
	_ = rate
	return claims
}

func sumOperator(claims []types.MintClaim, mints []math.Int, operator string) (got, total math.Int) {
	got = math.ZeroInt()
	total = math.ZeroInt()
	for i, c := range claims {
		total = total.Add(mints[i])
		if c.Operator == operator {
			got = got.Add(mints[i])
		}
	}
	return got, total
}
