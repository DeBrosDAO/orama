package stategen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlan_validate(t *testing.T) {
	require.NoError(t, Plan{Nullifiers: 1}.Validate())
	require.NoError(t, Plan{Keys: 1}.Validate())
	require.Error(t, Plan{}.Validate(), "a plan that writes nothing is refused")
	require.Error(t, Plan{Nullifiers: MaxTotal + 1}.Validate(), "a total past the bound is refused")
}

func TestPlan_stepsFollowTheLargestCategory(t *testing.T) {
	tests := map[string]struct {
		plan Plan
		want uint64
	}{
		"empty categories still take one step": {Plan{Keys: 2}, 1},
		"nullifiers decide":                    {Plan{Nullifiers: 10 * StepNullifiers}, 10},
		"one over the cap adds a step":         {Plan{Nullifiers: StepNullifiers + 1}, 2},
		"assets decide":                        {Plan{Nullifiers: StepNullifiers, Assets: 3 * StepAssets}, 3},
		"accounts decide":                      {Plan{Keys: 4 * StepAccounts * KeysPerAccount}, 4},
		"caps override the defaults":           {Plan{Nullifiers: 100, Caps: Caps{Nullifiers: 10}}, 10},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.plan.Steps())
		})
	}
}

func TestPlan_shareCoversEveryIndexOnce(t *testing.T) {
	for _, p := range []Plan{
		{Nullifiers: 1, Caps: Caps{Nullifiers: 1}},
		{Nullifiers: 97, Assets: 13, Keys: 50, Caps: Caps{Nullifiers: 10, Assets: 3, Accounts: 4}},
		{Nullifiers: 5, Assets: 1_000, Caps: Caps{Assets: 7}},
		{Nullifiers: 10 * StepNullifiers, Assets: 10, Keys: 7},
	} {
		for _, total := range []uint64{p.Nullifiers, p.Assets, p.Accounts()} {
			next := uint64(0)
			for step := uint64(0); step < p.Steps(); step++ {
				from, to := p.Share(total, step)
				require.Equal(t, next, from, "step %d of %+v starts where the previous one ended", step, p)
				require.LessOrEqual(t, from, to)
				next = to
			}
			require.Equal(t, total, next, "the steps of %+v write exactly %d", p, total)
		}
	}
}

func TestPlan_shareDoesNotOverflowAtTheBound(t *testing.T) {
	p := Plan{Nullifiers: MaxTotal, Caps: Caps{Nullifiers: MaxTotal / 4}}
	_, to := p.Share(MaxTotal, p.Steps()-1)
	require.Equal(t, MaxTotal, to)
}

func TestPlan_fingerprintSeparatesPlans(t *testing.T) {
	base := Plan{Seed: 1, Nullifiers: 10, Assets: 10, Keys: 10}
	require.Equal(t, base.Fingerprint(), base.Fingerprint())
	for _, other := range []Plan{
		{Seed: 2, Nullifiers: 10, Assets: 10, Keys: 10},
		{Seed: 1, Nullifiers: 11, Assets: 10, Keys: 10},
		{Seed: 1, Nullifiers: 10, Assets: 11, Keys: 10},
		{Seed: 1, Nullifiers: 10, Assets: 10, Keys: 12},
		{Seed: 1, Nullifiers: 10, Assets: 10, Keys: 10, Caps: Caps{Nullifiers: 5}},
	} {
		require.NotEqual(t, base.Fingerprint(), other.Fingerprint(), "%+v", other)
	}
}

func TestDerive_deterministicAndSeparated(t *testing.T) {
	require.Equal(t, Nullifier(7, 3), Nullifier(7, 3))
	require.NotEqual(t, Nullifier(7, 3), Nullifier(8, 3), "the seed changes the value")
	require.NotEqual(t, Nullifier(7, 3), Nullifier(7, 4))
	nf := Nullifier(7, 3)
	require.NotEqual(t, nf[:], AssetID(7, 3), "kinds do not share values")
	require.Len(t, AssetID(7, 3), 32)
	require.Len(t, Account(7, 3), 20)
	require.Equal(t, Owner(7, 5, 100), Owner(7, 5, 100))
}

func TestOwner_assetsShareOwnersAndTinyPlansWork(t *testing.T) {
	owners := map[string]bool{}
	for i := uint64(0); i < 1_000; i++ {
		owners[Owner(1, i, 1_000).String()] = true
	}
	require.Len(t, owners, 1_000/ownersPerAsset)
	require.NotPanics(t, func() { _ = Owner(1, 0, 1) }, "a plan of one asset has one owner")
}
