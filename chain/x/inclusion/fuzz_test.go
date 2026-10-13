package inclusion_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"sort"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

func fuzzSeedTxs() [][]byte {
	a, _ := inclusion.EncodeTx([]byte("alice"), 0, 10, []byte("payload"))
	b, _ := inclusion.EncodeTx([]byte("alice"), 1, 12, nil)
	c, _ := inclusion.EncodeTx([]byte("bob"), 0, 99, bytes.Repeat([]byte{7}, 40))
	return [][]byte{a, b, c}
}

func fuzzView() inclusion.View {
	return inclusion.View{
		Height: 7, Round: 1, BaseFee: 10, TotalPower: 100, Authenticated: true,
		Params: inclusion.Params{
			ListMaxBytes: 300, MaxEmbeddedListBytes: 600, MaxSenderBytes: 200,
			MaxBlockBytes: 500, MaxAnteAttempts: 6, MaxVerifyAttempts: 8,
		},
	}
}

// FuzzInclusionDecodeList: DecodeList never panics on a peer's vote-extension bytes. Whatever it
// accepts re-encodes to the same bytes (the encoding is canonical, so two nodes cannot see one
// list under two encodings), and a list that passes ValidateList is a fixed point of SelectList.
func FuzzInclusionDecodeList(f *testing.F) {
	seeds := fuzzSeedTxs()
	for _, txs := range [][][]byte{nil, seeds[:1], seeds, {[]byte("junk")}} {
		raw, err := inclusion.EncodeList(txs)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Add([]byte("ORIL\x01\xff\xff\xff\xff"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		txs, err := inclusion.DecodeList(raw)
		if err != nil {
			return
		}
		if len(raw) == 0 {
			if len(txs) != 0 {
				t.Fatal("empty payload decoded to transactions")
			}
			return
		}
		back, err := inclusion.EncodeList(txs)
		if err != nil || !bytes.Equal(back, raw) {
			t.Fatalf("decode then encode is not the identity (%v)", err)
		}
		v := fuzzView()
		if inclusion.ValidateList(v, txs) == nil {
			chosen := inclusion.SelectList(v, txs)
			if len(chosen) != len(txs) {
				t.Fatalf("SelectList drops %d of %d transactions of a list ValidateList accepts", len(txs)-len(chosen), len(txs))
			}
			for i := range txs {
				if !bytes.Equal(chosen[i], txs[i]) {
					t.Fatal("SelectList reorders a valid list")
				}
			}
		}
	})
}

// FuzzInclusionUnmarshalExtension: UnmarshalExtension never panics, and what it accepts marshals
// back to the same bytes, so the signature (over the fields) and the wire bytes name one thing.
func FuzzInclusionUnmarshalExtension(f *testing.F) {
	v := fuzzView()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, ed25519.SeedSize))
	for _, txs := range [][][]byte{nil, fuzzSeedTxs()} {
		ext, err := inclusion.BuildExtension(priv, 10, v, txs)
		if err != nil {
			f.Fatal(err)
		}
		raw, err := ext.MarshalBinary()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte("ORIN\x01"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		ext, err := inclusion.UnmarshalExtension(raw)
		if err != nil {
			return
		}
		back, err := ext.MarshalBinary()
		if err != nil || !bytes.Equal(back, raw) {
			t.Fatalf("unmarshal then marshal is not the identity (%v)", err)
		}
	})
}

// scriptTxs reads transactions from a byte script: a kind byte picks a well-formed transaction
// (sender, sequence, fee and payload from the next bytes) or a run of junk.
func scriptTxs(s []byte) [][]byte {
	var out [][]byte
	for len(s) >= 5 {
		kind, a, b, c, d := s[0], s[1], s[2], s[3], s[4]
		s = s[5:]
		if kind%4 == 0 {
			n := min(int(d)%9, len(s))
			out = append(out, bytes.Clone(s[:n]))
			s = s[n:]
			continue
		}
		raw, err := inclusion.EncodeTx([]byte{'s', a % 3}, uint64(b%4), uint64(c%32), bytes.Repeat([]byte{d}, int(d)%48))
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// FuzzInclusionProcess builds extended commits out of arbitrary listed transactions and checks
// the rules the proposer and the validators must agree on: Required is deterministic and sorted,
// every required transaction decodes and pays the base fee, the required bytes stay inside the
// block and per-sender caps, a block Assemble builds is accepted by Process, and dropping or
// moving the first required transaction is refused.
func FuzzInclusionProcess(f *testing.F) {
	f.Add([]byte{1, 0, 0, 20, 4, 1, 0, 1, 20, 4, 2, 1, 0, 30, 9}, uint8(3), uint8(0))
	f.Add([]byte{0, 0, 0, 0, 5, 1, 1, 1, 1, 1}, uint8(1), uint8(1))
	f.Add([]byte{}, uint8(2), uint8(0))
	f.Fuzz(func(t *testing.T, script []byte, split, proposerCut uint8) {
		v := fuzzView()
		all := scriptTxs(script)
		nExt := int(split)%4 + 1
		exts := make([]inclusion.Extension, nExt)
		for i := range exts {
			var list [][]byte
			for j := i; j < len(all); j += nExt {
				list = append(list, all[j])
			}
			exts[i] = inclusion.Extension{
				PubKey: bytes.Repeat([]byte{byte(i + 1)}, ed25519.PublicKeySize), Power: 40, Height: v.Height, Round: v.Round, Txs: list,
			}
		}
		commit, err := inclusion.PrepareCommit(v, exts)
		if err != nil {
			if !errors.Is(err, inclusion.ErrInsufficientPower) && !errors.Is(err, inclusion.ErrEmbeddedTooLarge) {
				t.Fatalf("PrepareCommit: %v", err)
			}
			return
		}
		need, err := inclusion.Required(v, commit)
		if err != nil {
			t.Fatalf("Required: %v", err)
		}
		again, _ := inclusion.Required(v, commit)
		if len(again) != len(need) {
			t.Fatal("Required is not deterministic")
		}
		total, perSender := 0, map[string]int{}
		for i, tx := range need {
			if !bytes.Equal(tx, again[i]) || (i > 0 && bytes.Compare(need[i-1], tx) >= 0) {
				t.Fatal("Required is not deterministic and sorted")
			}
			meta, err := inclusion.DecodeTx(tx)
			if err != nil || meta.Fee < v.BaseFee {
				t.Fatalf("a required transaction does not decode or underpays (%v)", err)
			}
			total += len(tx)
			perSender[inclusion.SenderKey(meta.Sender)] += len(tx)
		}
		if total > v.Params.MaxBlockBytes {
			t.Fatalf("required %d bytes, block budget %d", total, v.Params.MaxBlockBytes)
		}
		for sender, n := range perSender {
			if n > v.Params.MaxSenderBytes {
				t.Fatalf("sender %q has %d required bytes, cap %d", sender, n, v.Params.MaxSenderBytes)
			}
		}

		own := all[:min(int(proposerCut), len(all))]
		block, err := inclusion.Assemble(v, commit, own)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if err := inclusion.Process(v, commit, block); err != nil {
			t.Fatalf("Process refuses the block Assemble built: %v", err)
		}
		if len(need) > 0 {
			if inclusion.Process(v, commit, block[1:]) == nil {
				t.Fatal("a block without its first required transaction was accepted")
			}
			if len(need) > 1 {
				swapped := append([][]byte{}, block...)
				swapped[0], swapped[1] = swapped[1], swapped[0]
				if inclusion.Process(v, commit, swapped) == nil {
					t.Fatal("a block with the required transactions out of order was accepted")
				}
			}
		}
		sorted := sort.SliceIsSorted(block[:len(need)], func(i, j int) bool { return bytes.Compare(block[i], block[j]) < 0 })
		if !sorted {
			t.Fatal("the required prefix of the block is not in order")
		}
	})
}

// FuzzInclusionHasQuorum checks HasQuorum against exact big-integer arithmetic for every pair of
// int64s, including the ones whose product overflows.
func FuzzInclusionHasQuorum(f *testing.F) {
	f.Add(int64(2), int64(3))
	f.Add(int64(1)<<62, int64(1)<<62)
	f.Add(int64(-1), int64(5))
	f.Add(int64(9223372036854775807), int64(9223372036854775807))
	f.Fuzz(func(t *testing.T, power, total int64) {
		if got, want := inclusion.HasQuorum(power, total), quorumOracle(power, total); got != want {
			t.Fatalf("HasQuorum(%d, %d) = %v, exact arithmetic says %v", power, total, got, want)
		}
	})
}
