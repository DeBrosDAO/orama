package types

import (
	"fmt"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ValidateHeights checks an inclusive finalized-candidate range. The caller
// still has to check the end height against the current block.
func ValidateHeights(start, end int64) error {
	if start < 1 {
		return fmt.Errorf("start height must be at least 1, got %d", start)
	}
	if end < start {
		return fmt.Errorf("end height %d is below start height %d", end, start)
	}
	return nil
}

// ValidateBundleCID checks the bundle CID stored next to a range. It is an
// opaque content id, not parsed as a multihash.
func ValidateBundleCID(cid string) error {
	if cid == "" || len(cid) > MaxBundleCIDLen {
		return fmt.Errorf("bundle cid length must be 1-%d, got %d", MaxBundleCIDLen, len(cid))
	}
	for _, r := range cid {
		if r <= ' ' || r > '~' {
			return fmt.Errorf("bundle cid must be printable ASCII without spaces")
		}
	}
	return nil
}

// ValidateHash checks a SHA-256 digest.
func ValidateHash(name string, hash []byte) error {
	if len(hash) != HashLen {
		return fmt.Errorf("%s must be %d bytes, got %d", name, HashLen, len(hash))
	}
	return nil
}

// ValidateDealID checks that id is an x/storage deal id in canonical decimal
// (no sign, no leading zero, not zero). The keeper checks the deal itself.
func ValidateDealID(id string) error {
	if id == "" || len(id) > MaxDealIDLen {
		return fmt.Errorf("deal id length must be 1-%d, got %d", MaxDealIDLen, len(id))
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != id {
		return fmt.Errorf("deal id %q is not a decimal x/storage deal id", id)
	}
	return nil
}

// ValidateNodeID checks that a message names its archiver node.
func ValidateNodeID(nodeID string) error {
	if nodeID == "" || len(nodeID) > MaxNodeIDLen {
		return fmt.Errorf("node id length must be 1-%d, got %d", MaxNodeIDLen, len(nodeID))
	}
	return nil
}

// ValidateDealIDs checks a message's deal ids: non-empty, unique, each well formed.
func ValidateDealIDs(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("at least one deal id is required")
	}
	if len(ids) > MaxDealIDsPerRange {
		return fmt.Errorf("deal id count %d exceeds %d", len(ids), MaxDealIDsPerRange)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := ValidateDealID(id); err != nil {
			return err
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate deal id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// ValidateArchiver parses the signer address of an archive message.
func ValidateArchiver(archiver string) (sdk.AccAddress, error) {
	addr, err := sdk.AccAddressFromBech32(archiver)
	if err != nil {
		return nil, fmt.Errorf("invalid archiver address %q: %w", archiver, err)
	}
	return addr, nil
}

// ValidateAttestation checks MsgAttest fields and returns the signer.
func ValidateAttestation(archiver, nodeID string, start, end int64, bundleCID string, bundleHash, merkleRoot []byte) (sdk.AccAddress, error) {
	addr, err := ValidateArchiver(archiver)
	if err != nil {
		return nil, err
	}
	if err := ValidateNodeID(nodeID); err != nil {
		return nil, err
	}
	if err := ValidateHeights(start, end); err != nil {
		return nil, err
	}
	if err := ValidateBundleCID(bundleCID); err != nil {
		return nil, err
	}
	if err := ValidateHash("bundle_hash", bundleHash); err != nil {
		return nil, err
	}
	if err := ValidateHash("merkle_root", merkleRoot); err != nil {
		return nil, err
	}
	return addr, nil
}

// ValidateAttach checks MsgAttachReplicas fields and returns the signer.
func ValidateAttach(archiver, nodeID string, start, end int64, dealIDs []string) (sdk.AccAddress, error) {
	addr, err := ValidateArchiver(archiver)
	if err != nil {
		return nil, err
	}
	if err := ValidateNodeID(nodeID); err != nil {
		return nil, err
	}
	if err := ValidateHeights(start, end); err != nil {
		return nil, err
	}
	if err := ValidateDealIDs(dealIDs); err != nil {
		return nil, err
	}
	return addr, nil
}

// QuorumMet reports whether a range has enough distinct archivers and replica
// deal ids to be marked archived. Callers must already have rejected duplicates.
func QuorumMet(archivers, dealIDs int) bool {
	return archivers >= MinArchiverAttestations && dealIDs >= MinReplicaDeals
}
