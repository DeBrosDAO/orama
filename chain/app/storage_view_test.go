package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestRefuseStorageNodeErr_marksOnlyTheNodeRefusals(t *testing.T) {
	require.NoError(t, refuseStorageNodeErr(nil))

	for name, err := range map[string]error{
		"node not found":  fmt.Errorf("node n1: %w", nodestypes.ErrNotFound),
		"node not active": fmt.Errorf("node n1 is jailed: %w", nodestypes.ErrNotActive),
	} {
		got := refuseStorageNodeErr(err)
		require.ErrorIs(t, got, storagetypes.ErrItemRejected, name)
		require.ErrorIs(t, got, err, name)
		require.Equal(t, err.Error(), got.Error(), name)
	}

	for name, err := range map[string]error{
		"unexpected":      errors.New("counter went negative"),
		"undecodable":     fmt.Errorf("load node: %w", collections.ErrEncoding),
		"missing own row": fmt.Errorf("load index: %w", collections.ErrNotFound),
	} {
		require.NotErrorIs(t, refuseStorageNodeErr(err), storagetypes.ErrItemRejected, name)
	}
}
