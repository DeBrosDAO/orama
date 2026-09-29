package inclusion

import (
	"bytes"
	"math"
)

// State is the account view the sequential walk starts from.
// NextSequence is keyed by SenderKey. A missing sender has next sequence 0.
type State struct {
	NextSequence map[string]uint64
}

// Required returns the listed transactions that must occupy the front of the
// block, in lexicographic order of their raw bytes.
//
// The walk applies state as it goes. A transaction that does not decode, pays
// less than the base fee, or does not match the sender's next sequence is
// invalid and is skipped. A transaction that would push its sender over
// MaxSenderBytes, or the block over MaxBlockBytes, does not fit and is
// skipped. A transaction that fails View.Verify is skipped without cost to its
// claimed sender. Every other transaction that reaches View.Admit is charged
// to its sender's byte budget whether or not it is placed, and at most
// MaxAnteAttempts of them run Admit per block; the walk stops there.
//
// The rule is judged on the state the caller supplies. ProcessProposal has
// only the last committed state, while the transactions run after this
// block's BeginBlock. The judgement can therefore drift (the base fee moves
// by at most 12.5% a block, and BeginBlock pays rewards), and a required
// transaction may then fail at execution. The free space that allows is
// bounded, not eliminated: required transactions are drawn only from the
// deduplicated embedded set, so they never exceed MaxEmbeddedListBytes. Skipped transactions are not applied, so a later transaction is
// judged against the state left by the ones that were placed. The first
// same-sequence transaction in lexicographic order therefore wins, and a
// later one may be absent.
func Required(v View, c Commit) ([][]byte, error) {
	kept, err := accept(v, c.Extensions)
	if err != nil {
		return nil, err
	}
	return requiredTxs(kept, v), nil
}

// Process accepts block when every transaction Required would demand is a
// prefix of block, in that order. A proposer transaction before a listed
// transaction that was valid and fit is rejected. Listed transactions that
// were invalid or that did not fit may be absent, and may also appear after
// the required prefix.
func Process(v View, c Commit, block [][]byte) error {
	need, err := Required(v, c)
	if err != nil {
		return err
	}
	for i := range need {
		if i >= len(block) || !bytes.Equal(block[i], need[i]) {
			if !containsTx(block, need[i]) {
				return ErrMissing
			}
			return ErrOrder
		}
	}
	return nil
}

// Assemble builds a block whose prefix is Required, then appends proposer
// transactions that are not already in the block and that still fit in
// MaxBlockBytes. A transaction that appears only in an included extension
// is part of the prefix even when proposer does not contain it.
func Assemble(v View, c Commit, proposer [][]byte) ([][]byte, error) {
	need, err := Required(v, c)
	if err != nil {
		return nil, err
	}
	block := make([][]byte, 0, len(need)+len(proposer))
	used := 0
	seen := make(map[string]struct{}, len(need)+len(proposer))
	for _, tx := range need {
		cp := bytes.Clone(tx)
		block = append(block, cp)
		used += len(cp)
		seen[string(cp)] = struct{}{}
	}
	for _, tx := range proposer {
		if len(tx) == 0 {
			continue
		}
		if _, ok := seen[string(tx)]; ok {
			continue
		}
		if len(tx) > v.Params.MaxBlockBytes || used > v.Params.MaxBlockBytes-len(tx) {
			continue
		}
		cp := bytes.Clone(tx)
		block = append(block, cp)
		used += len(cp)
		seen[string(cp)] = struct{}{}
	}
	return block, nil
}

func requiredTxs(exts []Extension, v View) [][]byte {
	next := make(map[string]uint64, len(v.State.NextSequence))
	for key, seq := range v.State.NextSequence {
		next[key] = seq
	}
	// closed marks a sender whose last sequence was MaxUint64, so the
	// increment cannot wrap and make sequence 0 acceptable again.
	closed := make(map[string]bool)
	// tried is the bytes each sender has spent on attempts that passed the
	// cheap checks, placed or not. Charging attempts, not only placements,
	// stops a sender from making every node run its junk through the ante
	// chain again and again.
	tried := make(map[string]int)
	used := 0
	attempts := 0
	var need [][]byte
	for _, raw := range uniqueTxs(exts) {
		meta, err := v.decode(raw)
		if err != nil || meta.Fee < v.BaseFee {
			continue
		}
		key := SenderKey(meta.Sender)
		if closed[key] || meta.Sequence != next[key] {
			continue
		}
		if len(raw) > v.Params.MaxSenderBytes || tried[key] > v.Params.MaxSenderBytes-len(raw) {
			continue
		}
		if len(raw) > v.Params.MaxBlockBytes || used > v.Params.MaxBlockBytes-len(raw) {
			continue
		}
		// Verify runs before the charge, so a transaction that only claims a
		// sender cannot spend that sender's budget.
		if v.Verify != nil && !v.Verify(raw, meta) {
			continue
		}
		if v.Admit != nil {
			if attempts >= v.Params.MaxAnteAttempts {
				break
			}
			attempts++
		}
		tried[key] += len(raw)
		if v.Admit != nil && !v.Admit(raw, meta) {
			continue
		}
		need = append(need, bytes.Clone(raw))
		if meta.Sequence == math.MaxUint64 {
			closed[key] = true
		} else {
			next[key] = meta.Sequence + 1
		}
		used += len(raw)
	}
	return need
}

func containsTx(block [][]byte, tx []byte) bool {
	for _, have := range block {
		if bytes.Equal(have, tx) {
			return true
		}
	}
	return false
}
