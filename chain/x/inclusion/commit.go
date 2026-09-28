package inclusion

import (
	"bytes"
	"math"
	"sort"
)

// View is the consensus input for PrepareCommit, Process, and BuildExtension.
// TotalPower is the whole validator set, not only the extensions in hand.
// Each Extension.Power must be that key's power from the same set.
type View struct {
	Height     int64
	Round      int32
	TotalPower int64
	BaseFee    uint64
	State      State
	Params     Params
}

// Commit is the extended commit a proposer embeds in a block: the vote
// extensions it included, after invalid ones were dropped.
type Commit struct {
	Extensions []Extension
}

// EmbeddedSize is the number of deduplicated transaction bytes in the
// included extensions. A transaction present in more than one extension
// counts once. The result is what PrepareCommit compares with
// MaxEmbeddedListBytes.
func (c Commit) EmbeddedSize() int {
	n, ok := embeddedSize(c.Extensions)
	if !ok {
		return int(^uint(0) >> 1)
	}
	return n
}

// PrepareCommit builds the extended commit from the extensions the proposer
// is including. A bad signature, the wrong height or round, non-positive
// power, or a list over ListMaxBytes drops that extension. The same public
// key counts once, at its lesser power, and every valid list it signed stays
// in the union. The surviving power must be at least 2/3 of v.TotalPower.
// Deduplicated transaction bytes must fit in MaxEmbeddedListBytes.
func PrepareCommit(v View, exts []Extension) (Commit, error) {
	kept, err := accept(v, exts)
	if err != nil {
		return Commit{}, err
	}
	return Commit{Extensions: kept}, nil
}

func accept(v View, exts []Extension) ([]Extension, error) {
	if err := v.Params.Validate(); err != nil {
		return nil, err
	}
	kept := make([]Extension, 0, len(exts))
	for _, e := range exts {
		if e.Height != v.Height || e.Round != v.Round || e.Power <= 0 || !e.validSig() {
			continue
		}
		n, ok := listBytes(e.Txs)
		if !ok || n > v.Params.ListMaxBytes {
			continue
		}
		kept = append(kept, copyExtension(e))
	}
	sort.Slice(kept, func(i, j int) bool {
		c := bytes.Compare(kept[i].PubKey, kept[j].PubKey)
		if c != 0 {
			return c < 0
		}
		return bytes.Compare(kept[i].Signature, kept[j].Signature) < 0
	})
	if !powerOK(kept, v.TotalPower) {
		return nil, ErrInsufficientPower
	}
	size, ok := embeddedSize(kept)
	if !ok || size > v.Params.MaxEmbeddedListBytes {
		return nil, ErrEmbeddedTooLarge
	}
	return kept, nil
}

func powerOK(exts []Extension, total int64) bool {
	sum, overflow := votingPower(exts)
	if total <= 0 {
		return false
	}
	if overflow {
		return true
	}
	return HasQuorum(sum, total)
}

// votingPower sums one power per public key. Duplicates use the lesser power
// so a second copy cannot inflate the total.
func votingPower(exts []Extension) (int64, bool) {
	best := make(map[string]int64, len(exts))
	order := make([]string, 0, len(exts))
	for _, e := range exts {
		if e.Power <= 0 || len(e.PubKey) == 0 {
			continue
		}
		key := string(e.PubKey)
		if prev, ok := best[key]; ok {
			if e.Power < prev {
				best[key] = e.Power
			}
			continue
		}
		best[key] = e.Power
		order = append(order, key)
	}
	var sum int64
	for _, key := range order {
		p := best[key]
		if sum > math.MaxInt64-p {
			return 0, true
		}
		sum += p
	}
	return sum, false
}

func listBytes(txs [][]byte) (int, bool) {
	n := 0
	for _, tx := range txs {
		if len(tx) > int(^uint(0)>>1)-n {
			return 0, false
		}
		n += len(tx)
	}
	return n, true
}

func embeddedSize(exts []Extension) (int, bool) {
	return listBytes(uniqueTxs(exts))
}

func uniqueTxs(exts []Extension) [][]byte {
	all := make([][]byte, 0)
	for _, e := range exts {
		for _, tx := range e.Txs {
			if len(tx) == 0 {
				continue
			}
			all = append(all, tx)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return bytes.Compare(all[i], all[j]) < 0
	})
	out := make([][]byte, 0, len(all))
	for _, tx := range all {
		if len(out) > 0 && bytes.Equal(out[len(out)-1], tx) {
			continue
		}
		out = append(out, tx)
	}
	return out
}
