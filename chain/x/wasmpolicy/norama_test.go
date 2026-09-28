package wasmpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func TestRefuseNoramaWrapper(t *testing.T) {
	factoryNorama := "factory/orama1creator/norama"
	cases := []struct {
		name   string
		create string
		hold   []string
		reject bool
	}{
		{name: "create norama", create: params.BaseDenom, reject: true},
		{name: "create factory norama", create: factoryNorama, reject: true},
		{name: "hold factory norama", hold: []string{factoryNorama}, reject: true},
		{name: "create user token", create: "factory/orama1creator/uusd"},
		{name: "hold native norama", hold: []string{params.BaseDenom}},
		{name: "creator named norama", create: "factory/norama/uusd"},
		{name: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := wasmpolicy.RefuseNoramaWrapper(tc.create, tc.hold)
			if tc.reject {
				require.ErrorIs(t, err, types.ErrNoramaWrapper)
				return
			}
			require.NoError(t, err)
		})
	}
}
