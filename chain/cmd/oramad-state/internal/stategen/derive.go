package stategen

import (
	"crypto/sha256"
	"encoding/binary"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

// Domains keep the identifiers of different kinds apart: the same index never yields the same bytes
// as a nullifier and as an address.
const (
	domainNullifier byte = iota + 1
	domainAsset
	domainAccount
	domainOwner
)

// ownersPerAsset is how many assets one owner holds on average.
const ownersPerAsset = 10

// derive is SHA-256(seed || domain || index): spread over the key space like real identifiers, and
// a pure function of its inputs.
func derive(seed uint64, domain byte, index uint64) [sha256.Size]byte {
	buf := binary.BigEndian.AppendUint64(nil, seed)
	buf = append(buf, domain)
	buf = binary.BigEndian.AppendUint64(buf, index)
	return sha256.Sum256(buf)
}

// Nullifier is the i-th spent nullifier of a plan.
func Nullifier(seed, i uint64) [bundle.NodeLen]byte { return derive(seed, domainNullifier, i) }

// AssetID is the i-th decompressed asset's id.
func AssetID(seed, i uint64) []byte {
	id := derive(seed, domainAsset, i)
	return id[:]
}

// Account is the address of the i-th filler account.
func Account(seed, i uint64) sdk.AccAddress {
	h := derive(seed, domainAccount, i)
	return sdk.AccAddress(h[:20])
}

// Owner is the address that owns the asset-th of assets assets.
func Owner(seed, asset, assets uint64) sdk.AccAddress {
	h := derive(seed, domainOwner, asset%max(assets/ownersPerAsset, 1))
	return sdk.AccAddress(h[:20])
}
