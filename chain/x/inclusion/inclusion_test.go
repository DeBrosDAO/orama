package inclusion_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

func key(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func wide() inclusion.View {
	return inclusion.View{
		Height:     7,
		Round:      1,
		BaseFee:    10,
		TotalPower: 100,
		Params: inclusion.Params{
			ListMaxBytes:         1 << 20,
			MaxEmbeddedListBytes: 4 << 20,
			MaxSenderBytes:       1 << 20,
			MaxBlockBytes:        4 << 20,
			MaxAnteAttempts:      1 << 20,
			MaxVerifyAttempts:    1 << 20,
		},
	}
}

func mustTx(t *testing.T, sender string, seq, fee uint64, payload []byte) []byte {
	t.Helper()
	raw, err := inclusion.EncodeTx([]byte(sender), seq, fee, payload)
	if err != nil {
		t.Fatalf("EncodeTx: %v", err)
	}
	return raw
}

func mustExt(t *testing.T, priv ed25519.PrivateKey, power int64, v inclusion.View, txs ...[]byte) inclusion.Extension {
	t.Helper()
	ext, err := inclusion.BuildExtension(priv, power, v, txs)
	if err != nil {
		t.Fatalf("BuildExtension: %v", err)
	}
	return ext
}

func mustCommit(t *testing.T, v inclusion.View, exts ...inclusion.Extension) inclusion.Commit {
	t.Helper()
	c, err := inclusion.PrepareCommit(v, exts)
	if err != nil {
		t.Fatalf("PrepareCommit: %v", err)
	}
	return c
}

func sizedTx(t *testing.T, sender string, seq, fee uint64, total int) []byte {
	t.Helper()
	header := 2 + len(sender) + 16
	if total < header {
		t.Fatalf("sized tx total %d shorter than header %d", total, header)
	}
	raw := mustTx(t, sender, seq, fee, bytes.Repeat([]byte{'x'}, total-header))
	if len(raw) != total {
		t.Fatalf("sized tx length = %d, want %d", len(raw), total)
	}
	return raw
}

func quorumOracle(power, total int64) bool {
	if power <= 0 || total <= 0 {
		return false
	}
	var left, right big.Int
	left.Mul(big.NewInt(power), big.NewInt(3))
	right.Mul(big.NewInt(total), big.NewInt(2))
	return left.Cmp(&right) >= 0
}

func TestHasQuorum(t *testing.T) {
	cases := []struct {
		name  string
		power int64
		total int64
	}{
		{name: "zero power", power: 0, total: 10},
		{name: "zero total", power: 10, total: 0},
		{name: "negative power", power: -1, total: 10},
		{name: "negative total", power: 10, total: -5},
		{name: "one of one", power: 1, total: 1},
		{name: "exactly two of three", power: 2, total: 3},
		{name: "one of three", power: 1, total: 3},
		{name: "66 of 100", power: 66, total: 100},
		{name: "67 of 100", power: 67, total: 100},
		{name: "all of 100", power: 100, total: 100},
		{name: "max int exact boundary below", power: 6148914691236517204, total: math.MaxInt64},
		{name: "max int exact boundary", power: 6148914691236517205, total: math.MaxInt64},
		{name: "max int full", power: math.MaxInt64, total: math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := quorumOracle(tc.power, tc.total)
			if got := inclusion.HasQuorum(tc.power, tc.total); got != want {
				t.Fatalf("HasQuorum(%d, %d) = %v, want %v", tc.power, tc.total, got, want)
			}
		})
	}
}

func TestDefaultParams(t *testing.T) {
	p := inclusion.DefaultParams()
	if p.ListMaxBytes != 32*1024 {
		t.Fatalf("ListMaxBytes = %d, want %d", p.ListMaxBytes, 32*1024)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.ListMaxBytes > p.MaxEmbeddedListBytes {
		t.Fatalf("one full list (%d) does not fit in the embedded cap (%d)", p.ListMaxBytes, p.MaxEmbeddedListBytes)
	}
}

func TestParamsValidate(t *testing.T) {
	ok := inclusion.DefaultParams()
	cases := []struct {
		name   string
		params inclusion.Params
	}{
		{name: "zero list", params: func() inclusion.Params { p := ok; p.ListMaxBytes = 0; return p }()},
		{name: "zero embedded", params: func() inclusion.Params { p := ok; p.MaxEmbeddedListBytes = 0; return p }()},
		{name: "zero sender", params: func() inclusion.Params { p := ok; p.MaxSenderBytes = 0; return p }()},
		{name: "zero block", params: func() inclusion.Params { p := ok; p.MaxBlockBytes = 0; return p }()},
		{name: "zero verify attempts", params: func() inclusion.Params { p := ok; p.MaxVerifyAttempts = 0; return p }()},
		{name: "zero ante attempts", params: func() inclusion.Params { p := ok; p.MaxAnteAttempts = 0; return p }()},
		{name: "negative list", params: func() inclusion.Params { p := ok; p.ListMaxBytes = -1; return p }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.Validate()
			if !errors.Is(err, inclusion.ErrParams) {
				t.Fatalf("Validate() = %v, want ErrParams", err)
			}
		})
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTxCodec(t *testing.T) {
	cases := []struct {
		name    string
		sender  string
		seq     uint64
		fee     uint64
		payload []byte
		wantErr bool
	}{
		{name: "empty payload", sender: "a", seq: 0, fee: 0},
		{name: "values", sender: "alice", seq: 7, fee: 99, payload: []byte("memo")},
		{name: "long sender", sender: string(bytes.Repeat([]byte{'k'}, 255)), seq: math.MaxUint64, fee: math.MaxUint64, payload: []byte{0, 1}},
		{name: "empty sender", sender: "", wantErr: true},
		{name: "sender too long", sender: string(bytes.Repeat([]byte{'k'}, 256)), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := inclusion.EncodeTx([]byte(tc.sender), tc.seq, tc.fee, tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			meta, err := inclusion.DecodeTx(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(meta.Sender) != tc.sender || meta.Sequence != tc.seq || meta.Fee != tc.fee || !bytes.Equal(meta.Payload, tc.payload) {
				t.Fatalf("round trip = %+v", meta)
			}
			if inclusion.SenderKey(meta.Sender) != tc.sender {
				t.Fatalf("SenderKey = %q", inclusion.SenderKey(meta.Sender))
			}
		})
	}

	raw := mustTx(t, "a", 1, 2, []byte("z"))
	if _, err := inclusion.DecodeTx(raw[:4]); err == nil {
		t.Fatal("truncated transaction decoded")
	}
	bad := bytes.Clone(raw)
	bad[0] = 9
	if _, err := inclusion.DecodeTx(bad); err == nil {
		t.Fatal("bad version decoded")
	}
}

func TestBuildExtension_capsListAndSender(t *testing.T) {
	v := wide()
	v.Params = inclusion.DefaultParams()
	v.BaseFee = 1

	a := sizedTx(t, "a", 0, 1, 20*1024)
	b := sizedTx(t, "b", 0, 1, 20*1024)
	ext := mustExt(t, key(1), 10, v, b, a, a)
	if len(ext.Txs) != 1 || !bytes.Equal(ext.Txs[0], a) {
		t.Fatalf("packed txs = %d, want only the lexicographically smaller 20 KiB tx", len(ext.Txs))
	}
	got := 0
	for _, tx := range ext.Txs {
		got += len(tx)
	}
	if got > v.Params.ListMaxBytes {
		t.Fatalf("list bytes %d exceed %d", got, v.Params.ListMaxBytes)
	}

	tooBig := sizedTx(t, "a", 0, 1, v.Params.ListMaxBytes+1)
	empty := mustExt(t, key(1), 10, v, tooBig)
	if len(empty.Txs) != 0 {
		t.Fatalf("over-cap transaction was packed (%d)", len(empty.Txs))
	}

	v.Params.MaxSenderBytes = len(a)
	later := sizedTx(t, "a", 1, 1, 100)
	packed := mustExt(t, key(1), 10, v, a, later)
	if len(packed.Txs) != 1 || !bytes.Equal(packed.Txs[0], a) {
		t.Fatalf("sender cap packed %d txs, want only the first", len(packed.Txs))
	}
}

func TestExtensionWire(t *testing.T) {
	v := wide()
	tx := mustTx(t, "a", 0, 10, []byte("wire"))
	ext := mustExt(t, key(3), 40, v, tx)
	raw, err := ext.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	back, err := inclusion.UnmarshalExtension(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.Power != 0 {
		t.Fatalf("wire encoding carried power %d", back.Power)
	}
	back.Power = ext.Power
	if back.Height != ext.Height || back.Round != ext.Round || !bytes.Equal(back.PubKey, ext.PubKey) || !bytes.Equal(back.Signature, ext.Signature) {
		t.Fatal("round trip lost header fields")
	}
	if len(back.Txs) != 1 || !bytes.Equal(back.Txs[0], tx) {
		t.Fatal("round trip lost transactions")
	}
	v.TotalPower = 40
	if _, err := inclusion.PrepareCommit(v, []inclusion.Extension{back}); err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0)
	if _, err := inclusion.UnmarshalExtension(raw); err == nil {
		t.Fatal("trailing bytes were accepted")
	}
}

func TestPrepareCommit_sizeAfterDedup(t *testing.T) {
	v := wide()
	shared := mustTx(t, "s", 0, 10, bytes.Repeat([]byte{'s'}, 200))
	other := mustTx(t, "o", 0, 10, bytes.Repeat([]byte{'o'}, 200))
	a := mustExt(t, key(1), 50, v, shared)
	b := mustExt(t, key(2), 50, v, shared)
	v.TotalPower = 100
	v.Params.MaxEmbeddedListBytes = len(shared)
	if 2*len(shared) <= v.Params.MaxEmbeddedListBytes {
		t.Fatal("fixture does not require dedup")
	}

	c, err := inclusion.PrepareCommit(v, []inclusion.Extension{a, b})
	if err != nil {
		t.Fatalf("deduped commit was rejected: %v", err)
	}
	if c.EmbeddedSize() != len(shared) {
		t.Fatalf("EmbeddedSize = %d, want deduped %d", c.EmbeddedSize(), len(shared))
	}
	if len(c.Extensions) != 2 {
		t.Fatalf("extensions kept = %d, want both copies of the same tx", len(c.Extensions))
	}

	distinct := mustExt(t, key(2), 50, v, other)
	v.Params.MaxEmbeddedListBytes = len(shared) + len(other) - 1
	_, err = inclusion.PrepareCommit(v, []inclusion.Extension{a, distinct})
	if !errors.Is(err, inclusion.ErrEmbeddedTooLarge) {
		t.Fatalf("unique txs over the cap: %v, want ErrEmbeddedTooLarge", err)
	}

	v.Params.MaxEmbeddedListBytes = len(shared) + len(other)
	c, err = inclusion.PrepareCommit(v, []inclusion.Extension{a, distinct})
	if err != nil {
		t.Fatal(err)
	}
	if c.EmbeddedSize() != len(shared)+len(other) {
		t.Fatalf("EmbeddedSize = %d, want %d", c.EmbeddedSize(), len(shared)+len(other))
	}
}

func TestPrepareCommit_quorumAndDrops(t *testing.T) {
	v := wide()
	tx := mustTx(t, "a", 0, 10, []byte("listed"))
	onlyBad := mustTx(t, "z", 0, 10, []byte("bad-only"))

	cases := []struct {
		name    string
		total   int64
		build   func(t *testing.T) []inclusion.Extension
		wantErr error
		wantTx  [][]byte
	}{
		{
			name:  "exactly two thirds",
			total: 3,
			build: func(t *testing.T) []inclusion.Extension {
				return []inclusion.Extension{
					mustExt(t, key(1), 1, v),
					mustExt(t, key(2), 1, v),
				}
			},
		},
		{
			name:  "under two thirds",
			total: 3,
			build: func(t *testing.T) []inclusion.Extension {
				return []inclusion.Extension{mustExt(t, key(1), 1, v)}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "bad signature dropped and the rest still reach quorum",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				good := mustExt(t, key(1), 70, v, tx)
				bad := mustExt(t, key(2), 30, v, onlyBad)
				bad.Signature[0] ^= 0xff
				return []inclusion.Extension{bad, good}
			},
			wantTx: [][]byte{tx},
		},
		{
			name:  "bad signature drops the set under two thirds",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				good := mustExt(t, key(1), 50, v, tx)
				bad := mustExt(t, key(2), 50, v, onlyBad)
				bad.Signature = []byte{1, 2, 3}
				return []inclusion.Extension{good, bad}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "wrong height dropped",
			total: 10,
			build: func(t *testing.T) []inclusion.Extension {
				other := v
				other.Height = v.Height + 1
				return []inclusion.Extension{mustExt(t, key(1), 10, other, tx)}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "short public key does not panic",
			total: 10,
			build: func(t *testing.T) []inclusion.Extension {
				ext := mustExt(t, key(1), 10, v, tx)
				ext.PubKey = []byte{1, 2, 3, 4}
				return []inclusion.Extension{ext}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "negative power dropped",
			total: 10,
			build: func(t *testing.T) []inclusion.Extension {
				ext := mustExt(t, key(1), 10, v, tx)
				ext.Power = -5
				return []inclusion.Extension{ext}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "duplicate public key does not double power",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				ext := mustExt(t, key(1), 40, v, tx)
				return []inclusion.Extension{ext, ext}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "same key two lists count once and union the txs",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				first := mustExt(t, key(1), 100, v, tx)
				second := mustExt(t, key(1), 100, v, onlyBad)
				return []inclusion.Extension{first, second}
			},
			wantTx: [][]byte{tx, onlyBad},
		},
		{
			name:  "lesser duplicate power is the one counted",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				low := mustExt(t, key(1), 30, v, tx)
				high := mustExt(t, key(1), 100, v, onlyBad)
				return []inclusion.Extension{high, low}
			},
			wantErr: inclusion.ErrInsufficientPower,
		},
		{
			name:  "power sum above MaxInt64 still has quorum",
			total: math.MaxInt64,
			build: func(t *testing.T) []inclusion.Extension {
				return []inclusion.Extension{
					mustExt(t, key(1), math.MaxInt64, v),
					mustExt(t, key(2), 1, v),
				}
			},
		},
		{
			name:  "list over the cap is dropped",
			total: 100,
			build: func(t *testing.T) []inclusion.Extension {
				wideList := v
				wideList.Params.ListMaxBytes = 64 * 1024
				fat := mustExt(t, key(1), 30, wideList, sizedTx(t, "a", 0, 10, 20*1024), sizedTx(t, "b", 0, 10, 20*1024))
				empty := mustExt(t, key(2), 70, v)
				return []inclusion.Extension{fat, empty}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := v
			view.TotalPower = tc.total
			if tc.name == "list over the cap is dropped" {
				view.Params.ListMaxBytes = 32 * 1024
			}
			exts := tc.build(t)
			c, err := inclusion.PrepareCommit(view, exts)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PrepareCommit = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			need, err := inclusion.Required(view, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.wantTx) == 0 {
				if len(need) != 0 {
					t.Fatalf("required %d txs, want none", len(need))
				}
				return
			}
			if len(need) != len(tc.wantTx) {
				t.Fatalf("required %d txs, want %d", len(need), len(tc.wantTx))
			}
			for _, tx := range tc.wantTx {
				found := false
				for _, got := range need {
					if bytes.Equal(got, tx) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("required set missing %q", tx)
				}
			}
		})
	}
}

func TestProcess_prefix(t *testing.T) {
	v := wide()
	txA := mustTx(t, "a", 0, 10, []byte("a"))
	txB := mustTx(t, "b", 0, 10, []byte("b"))
	extra := mustTx(t, "p", 0, 10, []byte("proposer"))
	ext := mustExt(t, key(1), 100, v, txA, txB)
	c := mustCommit(t, v, ext)
	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 2 || !bytes.Equal(need[0], txA) || !bytes.Equal(need[1], txB) {
		t.Fatalf("required order = %q %q", need[0], need[1])
	}

	cases := []struct {
		name    string
		block   [][]byte
		wantErr error
	}{
		{name: "exact prefix", block: [][]byte{txA, txB}},
		{name: "proposer after listed", block: [][]byte{txA, txB, extra}},
		{name: "full block of listed txs", block: [][]byte{txA, txB}},
		{name: "swapped", block: [][]byte{txB, txA}, wantErr: inclusion.ErrOrder},
		{name: "proposer before listed", block: [][]byte{extra, txA, txB}, wantErr: inclusion.ErrOrder},
		{name: "second listed omitted", block: [][]byte{txA}, wantErr: inclusion.ErrMissing},
		{name: "only the later listed tx", block: [][]byte{txB}, wantErr: inclusion.ErrMissing},
		{name: "empty block", block: nil, wantErr: inclusion.ErrMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := v
			if tc.name == "full block of listed txs" {
				view.Params.MaxBlockBytes = len(txA) + len(txB)
				again, err := inclusion.Required(view, c)
				if err != nil {
					t.Fatal(err)
				}
				if len(again) != 2 {
					t.Fatalf("full budget dropped a listed tx: %d", len(again))
				}
			}
			err := inclusion.Process(view, c, tc.block)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Process = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestProcess_sameSequenceConflict(t *testing.T) {
	v := wide()
	first := mustTx(t, "alice", 4, 10, []byte("a"))
	second := mustTx(t, "alice", 4, 10, []byte("b"))
	if bytes.Compare(first, second) >= 0 {
		t.Fatal("fixture is not in lexicographic order")
	}
	ext := mustExt(t, key(1), 100, v, second, first)
	c := mustCommit(t, v, ext)
	v.State.NextSequence = map[string]uint64{inclusion.SenderKey([]byte("alice")): 4}

	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], first) {
		t.Fatalf("required = %d txs, want only the earlier same-sequence tx", len(need))
	}

	cases := []struct {
		name    string
		block   [][]byte
		wantErr error
	}{
		{name: "winner only", block: [][]byte{first}},
		{name: "loser may follow", block: [][]byte{first, second}},
		{name: "loser alone", block: [][]byte{second}, wantErr: inclusion.ErrMissing},
		{name: "loser before winner", block: [][]byte{second, first}, wantErr: inclusion.ErrOrder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := inclusion.Process(v, c, tc.block)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Process = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// A later sequence is required once the earlier one is applied, and a
	// gap is not required.
	next := mustTx(t, "alice", 5, 10, []byte("c"))
	gap := mustTx(t, "alice", 9, 10, []byte("g"))
	seqExt := mustExt(t, key(1), 100, v, next, first, gap)
	seqCommit := mustCommit(t, v, seqExt)
	got, err := inclusion.Required(v, seqCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !bytes.Equal(got[0], first) || !bytes.Equal(got[1], next) {
		t.Fatalf("sequential required set = %d, want sequence 4 then 5", len(got))
	}
	if err := inclusion.Process(v, seqCommit, [][]byte{first}); !errors.Is(err, inclusion.ErrMissing) {
		t.Fatalf("omitting the next sequence: %v", err)
	}
}

func TestProcess_underpayMayBeAbsent(t *testing.T) {
	v := wide()
	build := v
	build.BaseFee = 0
	under := mustTx(t, "a", 0, 1, []byte("cheap"))
	paid := mustTx(t, "b", 0, 10, []byte("paid"))
	ext := mustExt(t, key(1), 100, build, under, paid)
	c := mustCommit(t, v, ext)

	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], paid) {
		t.Fatalf("required set = %d, want only the tx that meets the base fee", len(need))
	}
	cases := []struct {
		name    string
		block   [][]byte
		wantErr error
	}{
		{name: "underpay absent", block: [][]byte{paid}},
		{name: "underpay after the required tx", block: [][]byte{paid, under}},
		{name: "underpay before the paid tx", block: [][]byte{under, paid}, wantErr: inclusion.ErrOrder},
		{name: "paid tx omitted", block: [][]byte{under}, wantErr: inclusion.ErrMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := inclusion.Process(v, c, tc.block)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Process = %v, want %v", err, tc.wantErr)
			}
		})
	}

	onlyUnder := mustExt(t, key(1), 100, build, under)
	only := mustCommit(t, v, onlyUnder)
	if err := inclusion.Process(v, only, nil); err != nil {
		t.Fatalf("block with no paid listed tx was rejected: %v", err)
	}
	if err := inclusion.Process(v, only, [][]byte{under}); err != nil {
		t.Fatalf("underpay in an otherwise empty requirement was rejected: %v", err)
	}
}

func TestProcess_senderByteCap(t *testing.T) {
	v := wide()
	build := v
	// Equal sender lengths keep lexicographic order aligned with sequence:
	// the length prefix would otherwise sort a shorter sender first.
	first := mustTx(t, "ann", 0, 10, bytes.Repeat([]byte{'a'}, 40))
	second := mustTx(t, "ann", 1, 10, bytes.Repeat([]byte{'b'}, 40))
	other := mustTx(t, "bob", 0, 10, []byte("bob"))
	ext := mustExt(t, key(1), 100, build, first, second, other)
	v.Params.MaxSenderBytes = len(first)
	c := mustCommit(t, v, ext)

	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 2 || !bytes.Equal(need[0], first) || !bytes.Equal(need[1], other) {
		t.Fatalf("required %d txs, want ann's first tx and bob", len(need))
	}
	if err := inclusion.Process(v, c, [][]byte{first, other}); err != nil {
		t.Fatal(err)
	}
	if err := inclusion.Process(v, c, [][]byte{first, other, second}); err != nil {
		t.Fatalf("tx past the sender cap may appear after the prefix: %v", err)
	}
	if err := inclusion.Process(v, c, [][]byte{first}); !errors.Is(err, inclusion.ErrMissing) {
		t.Fatalf("dropping the other sender: %v", err)
	}
}

func TestProcess_fitAfterEarlierListed(t *testing.T) {
	v := wide()
	big := mustTx(t, "a", 0, 10, bytes.Repeat([]byte{0x01}, 80))
	small := mustTx(t, "b", 0, 10, []byte{0x02})
	if bytes.Compare(big, small) >= 0 {
		t.Fatal("larger tx does not sort first")
	}
	ext := mustExt(t, key(1), 100, v, big, small)
	v.Params.MaxBlockBytes = len(small)
	c := mustCommit(t, v, ext)

	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], small) {
		t.Fatalf("required = %d, want the later tx that still fits", len(need))
	}
	cases := []struct {
		name    string
		block   [][]byte
		wantErr error
	}{
		{name: "earlier tx that does not fit may be absent", block: [][]byte{small}},
		{name: "earlier tx before the one that fits", block: [][]byte{big, small}, wantErr: inclusion.ErrOrder},
		{name: "only the tx that does not fit", block: [][]byte{big}, wantErr: inclusion.ErrMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := inclusion.Process(v, c, tc.block)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Process = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Both fit when the budget is exactly their sum: a full block is accepted.
	v.Params.MaxBlockBytes = len(big) + len(small)
	both, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 {
		t.Fatalf("both txs fit in %d bytes, required %d", v.Params.MaxBlockBytes, len(both))
	}
	if err := inclusion.Process(v, c, [][]byte{big, small}); err != nil {
		t.Fatalf("full block rejected: %v", err)
	}
}

func TestDroppingExtensionKeepsTxThatRemains(t *testing.T) {
	v := wide()
	v.TotalPower = 30
	shared := mustTx(t, "s", 0, 10, []byte("shared"))
	only := mustTx(t, "o", 0, 10, []byte("only-honest"))
	other := mustTx(t, "u", 0, 10, []byte("other"))
	honest := mustExt(t, key(1), 10, v, shared, only)
	also := mustExt(t, key(2), 10, v, shared)
	rest := mustExt(t, key(3), 10, v, other)

	// The proposer drops the first honest extension. shared is still listed
	// by the second, and only-honest is not.
	c := mustCommit(t, v, also, rest)
	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	has := func(tx []byte) bool {
		for _, got := range need {
			if bytes.Equal(got, tx) {
				return true
			}
		}
		return false
	}
	if !has(shared) || !has(other) || has(only) {
		t.Fatalf("required set is not the union of the included lists")
	}
	if err := inclusion.Process(v, c, [][]byte{other}); !errors.Is(err, inclusion.ErrMissing) {
		t.Fatalf("skipping a tx that remains after a dropped extension: %v", err)
	}
	block, err := inclusion.Assemble(v, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := inclusion.Process(v, c, block); err != nil {
		t.Fatal(err)
	}

	// The dropped extension's private tx is required when that extension is
	// included, so its absence above is because the extension was dropped.
	full := mustCommit(t, v, honest, also, rest)
	fullNeed, err := inclusion.Required(v, full)
	if err != nil {
		t.Fatal(err)
	}
	foundOnly := false
	for _, got := range fullNeed {
		if bytes.Equal(got, only) {
			foundOnly = true
		}
	}
	if !foundOnly {
		t.Fatal("including the dropped extension did not restore its transaction")
	}
}

func TestRequired_unionIncludesTxProposerDoesNotHave(t *testing.T) {
	v := wide()
	secret := mustTx(t, "s", 0, 10, []byte("seen-by-validators-only"))
	private := mustTx(t, "p", 0, 10, []byte("proposer-private"))
	ext := mustExt(t, key(1), 100, v, secret)
	c := mustCommit(t, v, ext)

	need, err := inclusion.Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], secret) {
		t.Fatal("required set is not the included list")
	}
	block, err := inclusion.Assemble(v, c, [][]byte{private})
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != 2 || !bytes.Equal(block[0], secret) || !bytes.Equal(block[1], private) {
		t.Fatalf("assemble put the extension-only tx somewhere other than the front: %d txs", len(block))
	}
	if err := inclusion.Process(v, c, block); err != nil {
		t.Fatal(err)
	}
	if err := inclusion.Process(v, c, [][]byte{private}); !errors.Is(err, inclusion.ErrMissing) {
		t.Fatalf("proposer's private set alone: %v", err)
	}
}

func TestProcess_doesNotMutateState(t *testing.T) {
	v := wide()
	tx := mustTx(t, "alice", 3, 10, []byte("x"))
	ext := mustExt(t, key(1), 100, v, tx)
	c := mustCommit(t, v, ext)
	v.State.NextSequence = map[string]uint64{"alice": 3}
	if err := inclusion.Process(v, c, [][]byte{tx}); err != nil {
		t.Fatal(err)
	}
	if v.State.NextSequence["alice"] != 3 {
		t.Fatalf("state sequence changed to %d", v.State.NextSequence["alice"])
	}
}

func TestAssemble_fullBlockLeavesNoRoom(t *testing.T) {
	v := wide()
	txA := mustTx(t, "a", 0, 10, []byte("a"))
	txB := mustTx(t, "b", 0, 10, []byte("b"))
	extra := mustTx(t, "p", 0, 10, []byte("extra"))
	ext := mustExt(t, key(1), 100, v, txA, txB)
	c := mustCommit(t, v, ext)
	v.Params.MaxBlockBytes = len(txA) + len(txB)
	block, err := inclusion.Assemble(v, c, [][]byte{extra})
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != 2 || !bytes.Equal(block[0], txA) || !bytes.Equal(block[1], txB) {
		t.Fatalf("full block length = %d", len(block))
	}
	if err := inclusion.Process(v, c, block); err != nil {
		t.Fatalf("full block rejected: %v", err)
	}
}
