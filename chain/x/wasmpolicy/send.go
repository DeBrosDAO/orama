package wasmpolicy

import "github.com/DeBrosOfficial/network/chain/x/fees/types"

// FeeEarningsModules are the module accounts a contract may bank-send norama to.
// fees holds earnings; fees_deposits holds state deposits. A user account is not on this list.
func FeeEarningsModules() []string {
	return []string{types.ModuleName, types.DepositsModuleName}
}
