package policy

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

func TestNoramaSendRestriction(t *testing.T) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount("orama", "oramapub")

	module := authtypes.NewModuleAddress("emission")
	modules := map[string]bool{module.String(): true}
	fn := NoramaSendRestriction(modules)
	from := sdk.AccAddress(bytes.Repeat([]byte{1}, 20))
	to := sdk.AccAddress(bytes.Repeat([]byte{2}, 20))
	one := sdk.NewCoins(sdk.NewInt64Coin(BaseDenom, 1))

	if _, err := fn(t.Context(), from, to, one); err != ErrPublicPayment {
		t.Fatalf("user send: %v", err)
	}
	if _, err := fn(t.Context(), module, to, one); err != nil {
		t.Fatalf("module send: %v", err)
	}
	if _, err := fn(t.Context(), from, to, sdk.NewCoins(sdk.NewInt64Coin("ufoo", 1))); err != nil {
		t.Fatalf("other denom: %v", err)
	}
}
