package app

import (
	"context"
	"testing"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

type recordingFunder struct{ calls []string }

func (f *recordingFunder) FundSpendFromEarnings(_ context.Context, _ sdk.AccAddress, denom string, _ math.Int) error {
	f.calls = append(f.calls, denom)
	return nil
}

// Earnings are norama only, so a bond in another denom is not topped up from them: the staking
// handler gets it untouched and refuses it.
func TestEarningsFundedStaking_onlyTopsUpTheBaseDenom(t *testing.T) {
	funder := &recordingFunder{}
	s := earningsFundedStaking{funder: funder}
	addr := sdk.AccAddress("staking_topup_signer")

	if err := s.fund(context.Background(), addr, sdk.NewCoin("ustake", math.NewInt(5))); err != nil {
		t.Fatal(err)
	}
	if len(funder.calls) != 0 {
		t.Fatalf("a %v bond reached the earnings funder", funder.calls)
	}
	if err := s.fund(context.Background(), addr, sdk.NewCoin(params.BaseDenom, math.NewInt(5))); err != nil {
		t.Fatal(err)
	}
	if len(funder.calls) != 1 || funder.calls[0] != params.BaseDenom {
		t.Fatalf("funder calls = %v, want one for %s", funder.calls, params.BaseDenom)
	}
}
