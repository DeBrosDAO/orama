package inclusion_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

const (
	// benchValidators is the committee whose lists the walk reads.
	benchValidators = 100
	// benchTxsEach is how many transactions each validator lists.
	benchTxsEach = 10
	// benchTxBytes is the size of one listed transaction: a bank send is about this big.
	benchTxBytes = 200
	// benchBaseFee is the fee every benchmark transaction pays.
	benchBaseFee = uint64(10)
)

// benchSender is a distinct 8-byte sender per index.
func benchSender(i int) []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(i))
}

// benchLists builds, for each validator, benchTxsEach transactions. With shared they all list the
// same transactions (a common mempool); otherwise each lists its own.
func benchLists(b *testing.B, shared bool) [][][]byte {
	b.Helper()
	lists := make([][][]byte, benchValidators)
	for v := range lists {
		for t := 0; t < benchTxsEach; t++ {
			sender := t
			if !shared {
				sender = v*benchTxsEach + t
			}
			header := 2 + len(benchSender(sender)) + 16
			raw, err := inclusion.EncodeTx(benchSender(sender), 0, benchBaseFee, bytes.Repeat([]byte{'x'}, benchTxBytes-header))
			if err != nil {
				b.Fatal(err)
			}
			lists[v] = append(lists[v], raw)
		}
	}
	return lists
}

// benchWalk returns the view and commit the sequential walk runs on: the whole committee has listed,
// the sender's next sequence is applied by Admit as the walk goes (sequences is that state), and Verify is a constant-time check.
func benchWalk(b *testing.B, shared bool) (view inclusion.View, commit inclusion.Commit, sequences map[string]uint64) {
	b.Helper()
	sequences = map[string]uint64{}
	view = inclusion.View{
		Height: 7, Round: 0, BaseFee: benchBaseFee, TotalPower: benchValidators,
		Params:        inclusion.DefaultParams(),
		State:         inclusion.State{NextSequence: sequences},
		Authenticated: true,
		Verify:        func(_ []byte, meta inclusion.Meta) bool { return len(meta.Sender) > 0 },
	}
	view.Admit = func(_ []byte, meta inclusion.Meta) bool {
		key := inclusion.SenderKey(meta.Sender)
		if sequences[key] != meta.Sequence {
			return false
		}
		sequences[key] = meta.Sequence + 1
		return true
	}
	exts := make([]inclusion.Extension, 0, benchValidators)
	for v, list := range benchLists(b, shared) {
		priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{byte(v + 1)}, ed25519.SeedSize))
		ext, err := inclusion.BuildExtension(priv, 1, view, list)
		if err != nil {
			b.Fatal(err)
		}
		exts = append(exts, ext)
	}
	commit, err := inclusion.PrepareCommit(view, exts)
	if err != nil {
		b.Fatal(err)
	}
	return view, commit, sequences
}

// BenchmarkRequired is the inclusion rule's walk: the verify and ante pass over the lists of a
// 100-validator committee, each validator listing 10 transactions of 200 bytes, either the same
// 10 (a shared mempool) or its own.
func BenchmarkRequired(b *testing.B) {
	for _, shared := range []bool{true, false} {
		name := fmt.Sprintf("validators=%d/txs-each=%d/shared=%t", benchValidators, benchTxsEach, shared)
		b.Run(name, func(b *testing.B) {
			view, commit, sequences := benchWalk(b, shared)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				clear(sequences)
				need, err := inclusion.Required(view, commit)
				if err != nil {
					b.Fatal(err)
				}
				if len(need) == 0 {
					b.Fatal("the walk required no transaction")
				}
			}
		})
	}
}
