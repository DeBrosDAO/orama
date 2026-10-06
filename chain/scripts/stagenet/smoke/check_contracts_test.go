package main

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
)

func TestMissingCodes(t *testing.T) {
	require.Empty(t, missingCodes([]uint64{1, 2, 3, 4, 5, 6}, standardCodeIDs))
	require.Equal(t, []uint64{2, 5}, missingCodes([]uint64{1, 3, 4}, standardCodeIDs))
	require.Equal(t, standardCodeIDs, missingCodes(nil, standardCodeIDs))
}

func TestContractAddressFromEvents(t *testing.T) {
	events := []abci.Event{
		{Type: "message", Attributes: []abci.EventAttribute{{Key: "action", Value: "x"}}},
		{Type: "instantiate", Attributes: []abci.EventAttribute{{Key: "_contract_address", Value: "orama1contract"}, {Key: "code_id", Value: "1"}}},
	}
	addr, ok := contractAddressFromEvents(events)
	require.True(t, ok)
	require.Equal(t, "orama1contract", addr)

	_, ok = contractAddressFromEvents(events[:1])
	require.False(t, ok)
	_, ok = contractAddressFromEvents([]abci.Event{{Type: "instantiate", Attributes: []abci.EventAttribute{{Key: "_contract_address", Value: ""}}}})
	require.False(t, ok, "an empty address is not an address")
	_, ok = contractAddressFromEvents(nil)
	require.False(t, ok)
}

func TestParseCW20Balance(t *testing.T) {
	got, err := parseCW20Balance([]byte(`{"balance":"10"}`))
	require.NoError(t, err)
	require.Equal(t, "10", got)
	for _, bad := range []string{``, `{}`, `{"balance":""}`, `nope`, `{"balance":10}`} {
		_, err := parseCW20Balance([]byte(bad))
		require.Error(t, err, bad)
	}
}

func TestCW20InstantiateMsg_holdsTheSupplyForTheOperator(t *testing.T) {
	raw, err := cw20InstantiateMsg("orama1holder")
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"Stagenet Smoke","symbol":"SMK","decimals":6,"initial_balances":[{"address":"orama1holder","amount":"1000"}]}`, string(raw))
}

func TestNodeID(t *testing.T) {
	require.Equal(t, "stagenet-mew", nodeID("mew"))
}
