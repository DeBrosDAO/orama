package wasmbindings_test

import (
	"testing"

	"cosmossdk.io/errors"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
)

func TestShieldedBindingReturnsNotLinked(t *testing.T) {
	var binding wasmbindings.Shielded = wasmbindings.ShieldedBinding{}
	for name, err := range map[string]error{
		"shield":   binding.Shield("contract", "1"),
		"unshield": binding.ReceiveUnshield("contract", "1"),
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, err, wasmbindings.ErrNotLinked)
			require.True(t, errors.IsOf(err, wasmbindings.ErrNotLinked))
			require.Contains(t, err.Error(), wasmbindings.NotLinkedCode)
		})
	}
}
