package inclusion

import (
	"bytes"
	"math"
	"sort"
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
// to its sender's byte budget whether or not it is placed.
//
// Verify and Admit runs are budgeted per listing extension, so one validator's
// junk cannot use up what the others' transactions need. MaxVerifyAttempts and
// MaxAnteAttempts are the block's totals; they are divided evenly among the
// extensions that list anything (at least one run each), and a transaction is
// charged to the extension that lists it and has the most budget left (the
// first in public-key order on a tie). A transaction that every extension
// listing it can no longer afford is skipped, and the walk goes on. Junk
// therefore costs only the validators that list it: a validator listing only
// junk runs out of its own share and its remaining junk is skipped, while a
// valid transaction another validator lists is still verified and required.
// Work stays bounded by the totals (or by one run per listing extension when
// there are more of those than the total).
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

// candidate is one distinct listed transaction and the extensions that list it, as indexes into
// the sorted extension slice.
type candidate struct {
	raw     []byte
	listers []int
}

// budgets is one run counter per extension, spent by whichever lister has the most left.
type budgets []int

// spend charges one run to the extension in listers with the most budget left, the lowest index on a
// tie, and reports whether any of them could pay.
func (b budgets) spend(listers []int) bool {
	best := -1
	for _, i := range listers {
		if b[i] > 0 && (best == -1 || b[i] > b[best]) {
			best = i
		}
	}
	if best == -1 {
		return false
	}
	b[best]--
	return true
}

// splitBudget divides total among n listing extensions, at least one run each.
func splitBudget(total, n int) int {
	if n == 0 {
		return 0
	}
	if per := total / n; per > 0 {
		return per
	}
	return 1
}

// candidates returns the distinct transactions of exts in lexicographic order, each with the
// extensions that list it, and the number of extensions that list anything.
func candidates(exts []Extension) (list []candidate, listing int) {
	byTx := make(map[string][]int)
	for i, e := range exts {
		listed := false
		for _, tx := range e.Txs {
			if len(tx) == 0 {
				continue
			}
			listed = true
			key := string(tx)
			if have := byTx[key]; len(have) > 0 && have[len(have)-1] == i {
				continue
			}
			byTx[key] = append(byTx[key], i)
		}
		if listed {
			listing++
		}
	}
	list = make([]candidate, 0, len(byTx))
	for key, listers := range byTx {
		list = append(list, candidate{raw: []byte(key), listers: listers})
	}
	sort.Slice(list, func(i, j int) bool { return bytes.Compare(list[i].raw, list[j].raw) < 0 })
	return list, listing
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
	list, listing := candidates(exts)
	verifies := make(budgets, len(exts))
	attempts := make(budgets, len(exts))
	for i := range exts {
		verifies[i] = splitBudget(v.Params.MaxVerifyAttempts, listing)
		attempts[i] = splitBudget(v.Params.MaxAnteAttempts, listing)
	}
	var need [][]byte
	for _, cand := range list {
		raw := cand.raw
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
		// sender cannot spend that sender's budget. That also makes it free
		// for the sender of the junk, so it costs the validators that list it
		// a verification from their own share instead: signature checks per
		// proposal are bounded, and one validator's junk cannot use up the
		// others' (see DefaultMaxVerifyAttempts).
		if v.Verify != nil {
			if !verifies.spend(cand.listers) {
				continue
			}
			if !v.Verify(raw, meta) {
				continue
			}
		}
		if v.Admit != nil && !attempts.spend(cand.listers) {
			continue
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
