package keeper

import "cosmossdk.io/collections"

type (
	chunkMapKey  = collections.Triple[uint64, string, uint32]
	reportMapKey = collections.Pair[uint64, string]
	payoutMapKey = collections.Pair[uint64, []byte]
)

func chunkKey(epoch uint64, reporter string, index uint32) chunkMapKey {
	return collections.Join3(epoch, reporter, index)
}

func reportKey(epoch uint64, reporter string) reportMapKey {
	return collections.Join(epoch, reporter)
}

func payoutKey(epoch uint64, fingerprint []byte) payoutMapKey {
	return collections.Join(epoch, fingerprint)
}
