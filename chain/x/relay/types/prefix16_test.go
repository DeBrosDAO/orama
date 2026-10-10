package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestRelayPrefix16(t *testing.T) {
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "unidentified maps to the shared bucket", in: "", want: types.UnidentifiedPrefix16},
		{name: "canonical /16", in: "203.0.0.0/16", want: "203.0.0.0/16"},
		{name: "host address is truncated", in: "10.9.8.7", want: "10.9.0.0/16"},
		{name: "an ipv6 network shares the unidentified bucket", in: "2001:db8::/32", want: types.UnidentifiedPrefix16},
		{name: "garbage rejected", in: "not-a-network", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := types.RelayPrefix16(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
