package pagebound

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
)

func TestClamp_nil_request_is_the_sdk_default(t *testing.T) {
	got, err := Clamp(nil, 100)
	require.NoError(t, err)
	require.Equal(t, uint64(0), got.Limit)
}

func TestClamp_limit_is_bounded(t *testing.T) {
	cases := map[string]struct{ in, want uint64 }{
		"unset":      {0, 0},
		"below":      {7, 7},
		"at the cap": {100, 100},
		"above":      {5000, 100},
		"max uint64": {^uint64(0), 100},
	}
	for name, c := range cases {
		got, err := Clamp(&query.PageRequest{Limit: c.in}, 100)
		require.NoError(t, err, name)
		require.Equal(t, c.want, got.Limit, name)
	}
}

func TestClamp_total_count_is_never_computed(t *testing.T) {
	got, err := Clamp(&query.PageRequest{Limit: 5, CountTotal: true}, 100)
	require.NoError(t, err)
	require.False(t, got.CountTotal)
}

func TestClamp_keeps_key_and_reverse(t *testing.T) {
	got, err := Clamp(&query.PageRequest{Key: []byte("k"), Reverse: true, Limit: 5}, 100)
	require.NoError(t, err)
	require.Equal(t, []byte("k"), got.Key)
	require.True(t, got.Reverse)
}

func TestClamp_offset_is_refused(t *testing.T) {
	_, err := Clamp(&query.PageRequest{Offset: 3}, 100)
	require.ErrorIs(t, err, ErrOffset)
}
