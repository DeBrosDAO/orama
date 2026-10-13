package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app"
)

// x/power reads every bonded validator's key as raw ed25519 and returns an error from EndBlock
// (a halt) for any other type. staking accepts whatever types the consensus params list, so a
// genesis that lists secp256k1 hands every user a way to halt the chain with one
// MsgCreateValidator. ValidateConsensusParams must refuse any validator key type but ed25519 on every
// chain but a localnet.
func TestValidateConsensusParams_onlyEd25519ValidatorKeysOutsideALocalnet(t *testing.T) {
	for _, chainID := range []string{productionChainID, stagenetChainID, devnetChainID} {
		cp := plannedConsensus()
		cp.Validator.PubKeyTypes = []string{"ed25519", "secp256k1"}
		require.ErrorContains(t, app.ValidateConsensusParams(chainID, cp), "validator.pub_key_types", chainID)
	}
}
