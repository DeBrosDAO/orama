package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/nodes and of the module account that holds
	// role bonds and unbonding escrow.
	ModuleName = "nodes"

	// StoreKey is the store key for x/nodes.
	StoreKey = ModuleName

	// DepositFieldOverhead is added to a record's measured proto size when
	// locking its state deposit. deposit_bytes and deposit_parts are zeroed
	// for the measurement (their values depend on the size), and a node's
	// capacity-index flags may be written immediately afterwards. 32 bytes
	// covers the largest varint encoding of those fields, tags included, so
	// the lock is never short of the record that is actually stored.
	DepositFieldOverhead = 32
)

var (
	ParamsKey           = collections.NewPrefix(0)
	OperatorPrefix      = collections.NewPrefix(1)
	NodePrefix          = collections.NewPrefix(2)
	ClusterPrefix       = collections.NewPrefix(3)
	UnbondingPrefix     = collections.NewPrefix(4)
	UnbondingTimePrefix = collections.NewPrefix(5)
	UnbondingNodePrefix = collections.NewPrefix(6)
	NextUnbondingPrefix = collections.NewPrefix(7)
	RevokedPrefix       = collections.NewPrefix(8)
	LivePubkeyPrefix    = collections.NewPrefix(9)
	ServiceDayPrefix    = collections.NewPrefix(10)
	FreeCapacityPrefix  = collections.NewPrefix(11)
	StorageDirtyPrefix  = collections.NewPrefix(12)
	HotKeyPrefix        = collections.NewPrefix(13)
	LiveIPPrefix        = collections.NewPrefix(14)
	NameOwnerPrefix     = collections.NewPrefix(15)
	NodeNamePrefix      = collections.NewPrefix(16)
)
