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

	// Decode reads a transaction's Meta. Nil means DecodeTx. The app supplies
	// its own so that real chain transactions can be listed.
	Decode func(raw []byte) (Meta, error)

	// Admit is the last check on a listed transaction that already passed
	// every other rule. It is called once per candidate, in walk order, and
	// may apply state so that a later candidate is judged against it. A false
	// return marks the transaction invalid, and it is skipped. It must be
	// deterministic. Nil admits everything.
	Admit func(raw []byte, meta Meta) bool

	// Verify is a cheap authenticity check on a listed transaction, run in the
	// sequential walk before the transaction is charged against its sender's
	// attempt budget and before Admit. It is how a transaction that claims a
	// sender it cannot sign for is refused without spending that sender's
	// budget or an Admit attempt. A false return skips the transaction. It
	// must be deterministic. Nil accepts everything.
	Verify func(raw []byte, meta Meta) bool

	// Authenticated says the caller already proved where each Extension came
	// from (CometBFT verified the vote extension signature), so the
	// Extension's own Signature is not checked. The app sets it; PubKey is
	// then only an identity key for de-duplication.
	Authenticated bool
}

func (v View) decode(raw []byte) (Meta, error) {
	if v.Decode != nil {
		return v.Decode(raw)
	}
	return DecodeTx(raw)
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
// in the union. The valid power must be at least 2/3 of v.TotalPower.
// Deduplicated transaction bytes must fit in MaxEmbeddedListBytes: a
// proposer (View.Authenticated false) that is over gets ErrEmbeddedTooLarge,
// while an authenticated commit is trimmed to the cap by a fixed rule after
// the quorum test, so trimming never removes quorum.
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
		if e.Height != v.Height || e.Round != v.Round || e.Power <= 0 {
			continue
		}
		if !v.Authenticated && !e.validSig() {
			continue
		}
		n, ok := listBytes(e.Txs)
		if !ok || n > v.Params.ListMaxBytes {
			continue
		}
		kept = append(kept, copyExtension(e))
	}
	// Quorum is judged on every valid extension in the commit, before any
	// trimming. Trimming only decides which transactions become required;
	// evaluating quorum on the survivors would let a valid commit fall under
	// 2/3 and leave every proposer unable to build a block.
	if !powerOK(kept, v.TotalPower) {
		return nil, ErrInsufficientPower
	}
	if v.Authenticated {
		kept = trimToEmbeddedCap(kept, v.Params.MaxEmbeddedListBytes)
	}
	sort.Slice(kept, func(i, j int) bool {
		c := bytes.Compare(kept[i].PubKey, kept[j].PubKey)
		if c != 0 {
			return c < 0
		}
		return bytes.Compare(kept[i].Signature, kept[j].Signature) < 0
	})
	size, ok := embeddedSize(kept)
	if !ok || size > v.Params.MaxEmbeddedListBytes {
		return nil, ErrEmbeddedTooLarge
	}
	return kept, nil
}

// trimToEmbeddedCap keeps extensions, highest power first (ties by public
// key), while the deduplicated transaction bytes stay within limit, and drops
// the rest. An app cannot choose which votes an extended commit contains, so
// an over-full set is cut by a fixed rule instead of failing the block. A
// dropped extension still counts toward quorum (see accept); only its
// transactions stop being required.
func trimToEmbeddedCap(exts []Extension, limit int) []Extension {
	order := append([]Extension(nil), exts...)
	sort.Slice(order, func(i, j int) bool {
		if order[i].Power != order[j].Power {
			return order[i].Power > order[j].Power
		}
		return bytes.Compare(order[i].PubKey, order[j].PubKey) < 0
	})
	seen := make(map[string]struct{})
	size := 0
	kept := make([]Extension, 0, len(order))
	for _, e := range order {
		added := 0
		fresh := make([]string, 0, len(e.Txs))
		for _, tx := range e.Txs {
			if _, ok := seen[string(tx)]; ok || len(tx) == 0 {
				continue
			}
			fresh = append(fresh, string(tx))
			added += len(tx)
		}
		if added > limit-size {
			continue
		}
		for _, tx := range fresh {
			seen[tx] = struct{}{}
		}
		size += added
		kept = append(kept, e)
	}
	return kept
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
