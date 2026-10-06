package inclusion

import (
	"bytes"
	"errors"
	"sort"
	"testing"
)

func mustTx(t *testing.T, sender string, seq, fee uint64, pad int) []byte {
	t.Helper()
	tx, err := EncodeTx([]byte(sender), seq, fee, bytes.Repeat([]byte{7}, pad))
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func listView() View {
	return View{BaseFee: 1, Params: DefaultParams(), Authenticated: true}
}

func TestList_roundTrip(t *testing.T) {
	txs := [][]byte{mustTx(t, "a", 0, 5, 3), mustTx(t, "b", 0, 5, 3)}
	raw, err := EncodeList(txs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !bytes.Equal(got[0], txs[0]) || !bytes.Equal(got[1], txs[1]) {
		t.Fatalf("round trip changed the list: %x", got)
	}
}

func TestList_emptyForms(t *testing.T) {
	if got, err := DecodeList(nil); err != nil || len(got) != 0 {
		t.Fatalf("no bytes is an empty list, got %v, %v", got, err)
	}
	raw, err := EncodeList(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeList(raw); err != nil || len(got) != 0 {
		t.Fatalf("an encoded empty list decodes to nothing, got %v, %v", got, err)
	}
	if err := ValidateList(listView(), nil); err != nil {
		t.Fatalf("an empty list is valid: %v", err)
	}
}

func TestDecodeList_rejectsMalformed(t *testing.T) {
	good, _ := EncodeList([][]byte{mustTx(t, "a", 0, 5, 3)})
	cases := map[string][]byte{
		"short":     good[:3],
		"magic":     append([]byte("XXXX"), good[4:]...),
		"version":   append(append(append([]byte{}, good[:4]...), 9), good[5:]...),
		"truncated": good[:len(good)-1],
		"trailing":  append(append([]byte{}, good...), 0),
		"count":     append(append([]byte{}, good[:5]...), 0xff, 0xff, 0xff, 0xff),
	}
	for name, raw := range cases {
		if _, err := DecodeList(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestValidateList_rules(t *testing.T) {
	a := mustTx(t, "a", 0, 5, 3)
	b := mustTx(t, "b", 0, 5, 3)
	lo, hi := a, b
	if bytes.Compare(lo, hi) > 0 {
		lo, hi = hi, lo
	}
	if err := ValidateList(listView(), [][]byte{lo, hi}); err != nil {
		t.Fatalf("sorted valid list rejected: %v", err)
	}
	cases := map[string][][]byte{
		"unsorted":   {hi, lo},
		"duplicate":  {lo, lo},
		"empty tx":   {{}},
		"undecoded":  {[]byte("junk")},
		"under fee":  {mustTx(t, "c", 0, 0, 1)},
		"list cap":   {mustTx(t, "d", 0, 5, DefaultListMaxBytes)},
		"sender cap": {mustTx(t, "e", 0, 5, DefaultMaxSenderBytes-40), mustTx(t, "e", 1, 5, 100)},
	}
	for name, txs := range cases {
		sorted := append([][]byte(nil), txs...)
		if name == "sender cap" && bytes.Compare(sorted[0], sorted[1]) > 0 {
			sorted[0], sorted[1] = sorted[1], sorted[0]
		}
		if err := ValidateList(listView(), sorted); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	bad := listView()
	bad.Params.ListMaxBytes = 0
	if err := ValidateList(bad, nil); !errors.Is(err, ErrParams) {
		t.Fatalf("zero params: got %v", err)
	}
}

func TestSelectList_appliesAdmitInOrder(t *testing.T) {
	a := mustTx(t, "a", 0, 5, 3)
	b := mustTx(t, "b", 0, 5, 3)
	junk := []byte("junk")
	v := listView()
	var seen [][]byte
	v.Admit = func(raw []byte, _ Meta) bool {
		seen = append(seen, raw)
		return bytes.Equal(raw, b)
	}
	got := SelectList(v, [][]byte{b, junk, a, a, nil})
	if len(got) != 1 || !bytes.Equal(got[0], b) {
		t.Fatalf("only the admitted tx is listed, got %x", got)
	}
	if len(seen) != 2 {
		t.Fatalf("Admit sees each decodable unique candidate once, saw %d", len(seen))
	}
}

func TestRequired_admitFalseSkipsAndDoesNotAdvanceSequence(t *testing.T) {
	first := mustTx(t, "s", 0, 5, 1)
	second := mustTx(t, "s", 1, 5, 1)
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 10
	v.Admit = func(raw []byte, _ Meta) bool { return !bytes.Equal(raw, first) }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 10, Height: 4, Txs: [][]byte{first, second}}}}
	need, err := Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 0 {
		t.Fatalf("the rejected first tx leaves the sender at sequence 0, so the second is not next: %x", need)
	}
}

func TestAccept_authenticatedSkipsSignatureButKeepsHeightAndQuorum(t *testing.T) {
	tx := mustTx(t, "s", 0, 5, 1)
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 1, 30
	unsigned := func(power int64, height int64) Extension {
		return Extension{PubKey: []byte{byte(power)}, Power: power, Height: height, Round: 1, Txs: [][]byte{tx}}
	}
	if _, err := PrepareCommit(v, []Extension{unsigned(20, 4)}); err != nil {
		t.Fatalf("2/3 of power with no signatures: %v", err)
	}
	if _, err := PrepareCommit(v, []Extension{unsigned(19, 4)}); !errors.Is(err, ErrInsufficientPower) {
		t.Fatalf("under 2/3: got %v", err)
	}
	if _, err := PrepareCommit(v, []Extension{unsigned(20, 3)}); !errors.Is(err, ErrInsufficientPower) {
		t.Fatalf("a wrong-height extension is dropped: got %v", err)
	}
	strict := v
	strict.Authenticated = false
	if _, err := PrepareCommit(strict, []Extension{unsigned(20, 4)}); !errors.Is(err, ErrInsufficientPower) {
		t.Fatalf("without Authenticated an unsigned extension is dropped: got %v", err)
	}
}

func TestAccept_authenticatedTrimsToTheEmbeddedCap(t *testing.T) {
	big1 := mustTx(t, "a", 0, 5, 100)
	big2 := mustTx(t, "b", 0, 5, 100)
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 31
	v.Params.MaxEmbeddedListBytes = len(big1) + 10
	exts := []Extension{
		{PubKey: []byte("x"), Power: 10, Height: 4, Txs: [][]byte{big1}},
		{PubKey: []byte("y"), Power: 20, Height: 4, Txs: [][]byte{big2}},
	}
	c, err := PrepareCommit(v, exts)
	if err != nil {
		t.Fatalf("30/31 of power is valid; trimming for the embedded cap must not remove quorum: %v", err)
	}
	if len(c.Extensions) != 1 || !bytes.Equal(c.Extensions[0].PubKey, []byte("y")) {
		t.Fatalf("only the highest power extension fits and stays: %+v", c.Extensions)
	}
	v.TotalPower = 46
	if _, err := PrepareCommit(v, exts); !errors.Is(err, ErrInsufficientPower) {
		t.Fatalf("30/46 is under 2/3 before any trimming: got %v", err)
	}
	v.TotalPower = 30
	c, err = PrepareCommit(v, exts)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Extensions) != 1 || !bytes.Equal(c.Extensions[0].PubKey, []byte("y")) {
		t.Fatalf("the highest power extension is kept: %+v", c.Extensions)
	}
	strict := v
	strict.Authenticated = false
	if _, err := PrepareCommit(strict, exts); err == nil {
		t.Fatal("without Authenticated nothing verifies")
	}
}

func TestRequired_quorumSurvivesTrimmingButOnlyKeptListsAreRequired(t *testing.T) {
	// Three equal validators each list a disjoint 100-byte transaction, and
	// the embedded cap fits one. All three are valid, so quorum holds; only
	// the tie-break winner's transaction is required.
	txs := [][]byte{mustTx(t, "a", 0, 5, 100), mustTx(t, "b", 0, 5, 100), mustTx(t, "c", 0, 5, 100)}
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 3
	v.Params.MaxEmbeddedListBytes = len(txs[0]) + 1
	var exts []Extension
	for i, tx := range txs {
		exts = append(exts, Extension{PubKey: []byte{byte('a' + i)}, Power: 1, Height: 4, Txs: [][]byte{tx}})
	}
	need, err := Required(v, Commit{Extensions: exts})
	if err != nil {
		t.Fatalf("a full valid commit must not stall the chain: %v", err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], txs[0]) {
		t.Fatalf("required = %x, want just the first validator's transaction", need)
	}
}

func TestRequired_verifyFailureDoesNotSpendTheClaimedSendersBudget(t *testing.T) {
	// A forged transaction claiming sender "s" sorts before the real one. It
	// fails Verify, so it must not use up the sender's attempt budget.
	forged := mustTx(t, "s", 0, 5, 60)
	real := mustTx(t, "s", 0, 5, 70)
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 1
	v.Params.MaxSenderBytes = len(real) + 5
	v.Verify = func(raw []byte, _ Meta) bool { return bytes.Equal(raw, real) }
	v.Admit = func([]byte, Meta) bool { return true }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 1, Height: 4, Txs: sortedTxs(forged, real)}}}
	need, err := Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], real) {
		t.Fatalf("required = %x, want the real transaction", need)
	}
}

func TestRequired_attemptsAreChargedToTheSender(t *testing.T) {
	// Two same-sequence txs of one sender: the first fails Admit, the second
	// would pass, but the first's bytes already used the sender's budget.
	a := mustTx(t, "s", 0, 5, 60)
	b := mustTx(t, "s", 0, 5, 61)
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 1
	v.Params.MaxSenderBytes = len(a) + 1
	admitted := 0
	v.Admit = func(raw []byte, _ Meta) bool { admitted++; return false }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 1, Height: 4, Txs: sortedTxs(a, b)}}}
	if _, err := Required(v, c); err != nil {
		t.Fatal(err)
	}
	if admitted != 1 {
		t.Fatalf("Admit ran %d times, want 1: a failed attempt still costs its bytes", admitted)
	}
}

func TestRequired_anteAttemptsAreCapped(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 1
	v.Params.MaxAnteAttempts = 3
	var txs [][]byte
	for i := 0; i < 10; i++ {
		txs = append(txs, mustTx(t, string(rune('a'+i)), 0, 5, 10))
	}
	ran := 0
	v.Admit = func([]byte, Meta) bool { ran++; return false }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 1, Height: 4, Txs: sortedTxs(txs...)}}}
	if _, err := Required(v, c); err != nil {
		t.Fatal(err)
	}
	if ran != 3 {
		t.Fatalf("Admit ran %d times, want the cap of 3", ran)
	}
}

func TestRequired_neverExceedsTheEmbeddedCap(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 4
	v.Params.MaxEmbeddedListBytes = 350
	v.Admit = func([]byte, Meta) bool { return true }
	var exts []Extension
	for i := 0; i < 4; i++ {
		exts = append(exts, Extension{PubKey: []byte{byte(i)}, Power: 1, Height: 4,
			Txs: [][]byte{mustTx(t, string(rune('a'+i)), 0, 5, 120)}})
	}
	need, err := Required(v, Commit{Extensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, tx := range need {
		total += len(tx)
	}
	if total > v.Params.MaxEmbeddedListBytes {
		t.Fatalf("required bytes %d exceed the embedded cap %d", total, v.Params.MaxEmbeddedListBytes)
	}
}

func sortedTxs(txs ...[]byte) [][]byte {
	out := append([][]byte(nil), txs...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

// Verify runs before a sender is charged, so junk that only names a real sender at its next
// sequence is free for whoever sends it. The walk stops after MaxVerifyAttempts of them: the node
// runs a bounded number of signature checks per proposal, and a valid transaction behind that much
// junk in byte order is not required (a proposer may still include it).
func TestRequired_verifyAttemptsAreCapped(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 1
	v.Params.MaxVerifyAttempts = 5
	// Twenty transactions naming one sender at its next sequence: distinct, so none is a duplicate.
	var txs [][]byte
	for i := 0; i < 20; i++ {
		txs = append(txs, mustTx(t, "victim", 0, 5, 10+i))
	}
	txs = sortedTxs(txs...)
	// The genuine one sorts last, behind all the junk.
	good := txs[len(txs)-1]
	verified := 0
	v.Verify = func(raw []byte, _ Meta) bool {
		verified++
		return bytes.Equal(raw, good)
	}
	v.Admit = func([]byte, Meta) bool { return true }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 1, Height: 4, Txs: txs}}}
	need, err := Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if verified != 5 {
		t.Fatalf("Verify ran %d times, want the cap of 5", verified)
	}
	for _, tx := range need {
		if bytes.Equal(tx, good) {
			t.Fatal("the walk went past the cap to a transaction behind the junk")
		}
	}

	v.Params.MaxVerifyAttempts = 20
	verified = 0
	need, err = Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], good) {
		t.Fatalf("with room for every verification the genuine transaction is required, got %d", len(need))
	}
}

func TestRequired_anHonestListStaysUnderTheVerifyCap(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 1
	v.Params.MaxVerifyAttempts = 10
	var txs [][]byte
	for i := 0; i < 10; i++ {
		txs = append(txs, mustTx(t, string(rune('a'+i)), 0, 5, 10+i))
	}
	v.Verify = func([]byte, Meta) bool { return true }
	v.Admit = func([]byte, Meta) bool { return true }
	c := Commit{Extensions: []Extension{{PubKey: []byte("k"), Power: 1, Height: 4, Txs: sortedTxs(txs...)}}}
	need, err := Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 10 {
		t.Fatalf("required %d of 10 honest transactions", len(need))
	}
}

func TestDefaultParams_verifyCapIsAtLeastTheAnteCap(t *testing.T) {
	p := DefaultParams()
	if p.MaxVerifyAttempts < p.MaxAnteAttempts {
		t.Fatalf("MaxVerifyAttempts %d is below MaxAnteAttempts %d: honest lists would be cut by the verify cap", p.MaxVerifyAttempts, p.MaxAnteAttempts)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

// One validator's junk must not use up the verifications another validator's transaction needs. The
// junk validator lists far more distinct junk than the block's verify total, all sorting before the
// honest validator's one valid transaction; before, the shared budget ran out inside the junk and the
// valid transaction was not required.
func TestRequired_oneValidatorsJunkDoesNotStarveAnotherValidatorsTransaction(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 2
	v.Params.MaxVerifyAttempts = 20
	var junk [][]byte
	for i := 0; i < 200; i++ {
		junk = append(junk, mustTx(t, "victim", 0, 5, 10+i))
	}
	junk = sortedTxs(junk...)
	// The valid transaction sorts behind every junk one.
	good := mustTx(t, "victim", 0, 5, 300)
	if bytes.Compare(good, junk[len(junk)-1]) < 0 {
		t.Fatal("test setup: the valid transaction must sort last")
	}
	verified := 0
	v.Verify = func(raw []byte, _ Meta) bool {
		verified++
		return bytes.Equal(raw, good)
	}
	v.Admit = func([]byte, Meta) bool { return true }
	c := Commit{Extensions: []Extension{
		{PubKey: []byte("a-junk"), Power: 1, Height: 4, Txs: junk[:100]},
		{PubKey: []byte("b-junk"), Power: 0, Height: 4, Txs: nil},
		{PubKey: []byte("z-honest"), Power: 1, Height: 4, Txs: [][]byte{good}},
	}}
	// Power 0 extensions are dropped by accept; keep the honest and the junk one only.
	c.Extensions = []Extension{c.Extensions[0], c.Extensions[2]}
	need, err := Required(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 1 || !bytes.Equal(need[0], good) {
		t.Fatalf("the honest validator's valid transaction is required, got %d transactions", len(need))
	}
	if verified > v.Params.MaxVerifyAttempts {
		t.Fatalf("Verify ran %d times, above the block total of %d", verified, v.Params.MaxVerifyAttempts)
	}
}

// Overlapping honest lists cost one run per distinct transaction, not one per validator listing it.
func TestRequired_sharedTransactionsAreChargedOnce(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 3
	v.Params.MaxVerifyAttempts = 30
	v.Params.MaxAnteAttempts = 30
	var txs [][]byte
	for i := 0; i < 30; i++ {
		txs = append(txs, mustTx(t, string(rune('a'+i%26))+string(rune('a'+i/26)), 0, 5, 10))
	}
	txs = sortedTxs(txs...)
	v.Verify = func([]byte, Meta) bool { return true }
	v.Admit = func([]byte, Meta) bool { return true }
	var exts []Extension
	for i := 0; i < 3; i++ {
		exts = append(exts, Extension{PubKey: []byte{byte(i)}, Power: 1, Height: 4, Txs: txs})
	}
	need, err := Required(v, Commit{Extensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 30 {
		t.Fatalf("required %d of 30 transactions every validator lists: shared transactions were charged more than once", len(need))
	}
}

// The total work stays bounded: a validator that lists only junk can spend its own share and no more.
func TestRequired_aJunkValidatorSpendsOnlyItsOwnShare(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 3, 0, 3
	v.Params.MaxVerifyAttempts = 30
	var junk [][]byte
	for i := 0; i < 100; i++ {
		junk = append(junk, mustTx(t, "victim", 0, 5, 10+i))
	}
	verified := 0
	v.Verify = func([]byte, Meta) bool { verified++; return false }
	v.Admit = func([]byte, Meta) bool { return true }
	c := Commit{Extensions: []Extension{
		{PubKey: []byte{1}, Power: 1, Height: 3, Txs: sortedTxs(junk...)},
		{PubKey: []byte{2}, Power: 1, Height: 3, Txs: [][]byte{mustTx(t, "other", 0, 5, 10)}},
		{PubKey: []byte{3}, Power: 1, Height: 3, Txs: [][]byte{mustTx(t, "third", 0, 5, 10)}},
	}}
	if _, err := Required(v, c); err != nil {
		t.Fatal(err)
	}
	// 30 runs over 3 listing extensions is 10 each: the junk validator's 100 cost 10 runs, the two
	// others one each.
	if verified != 12 {
		t.Fatalf("Verify ran %d times, want 12 (10 for the junk validator, 1 each for the others)", verified)
	}
}

// More listing validators than the block total still each get one run, so nobody is silenced.
func TestRequired_everyListingValidatorGetsAtLeastOneRun(t *testing.T) {
	v := listView()
	v.Height, v.Round, v.TotalPower = 4, 0, 4
	v.Params.MaxVerifyAttempts = 2
	v.Verify = func([]byte, Meta) bool { return true }
	v.Admit = func([]byte, Meta) bool { return true }
	var exts []Extension
	for i := 0; i < 4; i++ {
		exts = append(exts, Extension{PubKey: []byte{byte(i)}, Power: 1, Height: 4,
			Txs: [][]byte{mustTx(t, string(rune('a'+i)), 0, 5, 10)}})
	}
	need, err := Required(v, Commit{Extensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 4 {
		t.Fatalf("required %d of 4 transactions, one per listing validator", len(need))
	}
}
