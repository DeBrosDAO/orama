package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/relay.
	ModuleName = "relay"

	// StoreKey is the store key for x/relay.
	StoreKey = ModuleName
)

var (
	// ParamsKey is the collections key for genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// ReportersPrefix keys the reporter set by bech32 address.
	ReportersPrefix = collections.NewPrefix(1)
	// RelaysPrefix keys Relay records by RSA fingerprint.
	RelaysPrefix = collections.NewPrefix(2)
	// ChunksPrefix keys in-flight report chunks by (epoch, reporter, index).
	ChunksPrefix = collections.NewPrefix(3)
	// ReportsPrefix keys reassembled reports by (epoch, reporter).
	ReportsPrefix = collections.NewPrefix(4)
	// ActivationKey is the collections key for Activation.
	ActivationKey = collections.NewPrefix(5)
	// EpochResultsPrefix keys EpochResult by epoch number.
	EpochResultsPrefix = collections.NewPrefix(6)
	// PayoutsPrefix keys RelayPayout by (epoch, fingerprint).
	PayoutsPrefix = collections.NewPrefix(7)
	// NodeIndexPrefix keys a node id to its RSA fingerprint.
	NodeIndexPrefix = collections.NewPrefix(8)
)
