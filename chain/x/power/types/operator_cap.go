package types

import (
	"context"
	"sort"

	"cosmossdk.io/math"
)

// UnlinkedOperator is the operator every validator shares when its consensus key is not bound to a
// live x/nodes node: power that no registered operator answers for sits in one bucket, under one
// cap. Registering the node and binding the consensus key is how a validator leaves it, so
// splitting stake over validators never gains a slot by staying unregistered.
const UnlinkedOperator = "unlinked"

// OperatorResolver names the operator a validator's consensus key belongs to. x/nodes implements
// it: a node's "consensus" binding holds the validator's ed25519 consensus pubkey, signed by that
// key for the node's operator, so the operator of the node is the operator of the validator.
type OperatorResolver interface {
	// OperatorOfConsensusKey returns the account that registered the live node whose consensus
	// binding holds pubkey, and false when no live node binds it.
	OperatorOfConsensusKey(ctx context.Context, pubkey []byte) (string, bool, error)
}

// OperatorStake is one validator's bonded stake tagged with the operator it counts toward.
type OperatorStake struct {
	// OperatorAddress is the validator's bech32 valoper address.
	OperatorAddress string
	// Operator is the operator the validator belongs to: an x/nodes operator account, the
	// validator's own address when there is no operator registry, or UnlinkedOperator.
	Operator string
	// BondedTokens is the validator's bonded stake (norama), including delegations.
	BondedTokens math.Int
}

// ComputeOperatorCappedShares is ComputeCappedShares applied per operator: the validators of one
// operator are summed, the cap and the redistribution run over those sums, and each operator's
// share is split back over its validators pro rata to their stake. An operator therefore holds at
// most cap of the total however many validators it runs, and a validator never holds more than its
// operator does. An operator whose validators hold no stake splits its share equally.
//
// The result is aligned with stakes. It is a pure function of its arguments: operators are
// processed in sorted order, so the same input gives the same output on every node.
func ComputeOperatorCappedShares(stakes []OperatorStake, cap, maxRedistributionMultiplier math.LegacyDec) []math.LegacyDec {
	if len(stakes) == 0 {
		return nil
	}
	members := make(map[string][]int, len(stakes))
	for i, s := range stakes {
		members[s.Operator] = append(members[s.Operator], i)
	}
	operators := make([]string, 0, len(members))
	for op := range members {
		operators = append(operators, op)
	}
	sort.Strings(operators)

	sums := make([]ValidatorStake, len(operators))
	for i, op := range operators {
		total := math.ZeroInt()
		for _, idx := range members[op] {
			total = total.Add(stakes[idx].BondedTokens)
		}
		sums[i] = ValidatorStake{OperatorAddress: op, BondedTokens: total}
	}
	operatorShares := ComputeCappedShares(sums, cap, maxRedistributionMultiplier)

	out := make([]math.LegacyDec, len(stakes))
	for i, op := range operators {
		splitOperatorShare(stakes, members[op], sums[i].BondedTokens, operatorShares[i], out)
	}
	return out
}

// splitOperatorShare writes share, divided over the validators at idx pro rata to their stake, into
// out. total is the sum of those validators' stake.
func splitOperatorShare(stakes []OperatorStake, idx []int, total math.Int, share math.LegacyDec, out []math.LegacyDec) {
	if !total.IsPositive() {
		each := share.QuoInt64(int64(len(idx)))
		for _, i := range idx {
			out[i] = each
		}
		return
	}
	totalDec := math.LegacyNewDecFromInt(total)
	for _, i := range idx {
		out[i] = share.Mul(math.LegacyNewDecFromInt(stakes[i].BondedTokens)).Quo(totalDec)
	}
}

// CountOperators returns the number of distinct operators among the stakes' Operator fields.
func CountOperators(operators []string) uint64 {
	seen := make(map[string]struct{}, len(operators))
	for _, op := range operators {
		seen[op] = struct{}{}
	}
	return uint64(len(seen))
}
