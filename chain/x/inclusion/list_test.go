package inclusion

import (
	"bytes"
	"errors"
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
	if !errors.Is(err, ErrInsufficientPower) {
		t.Fatalf("the lower-power extension is dropped to fit, leaving 20/31, under 2/3: got err %v, ext %d", err, len(c.Extensions))
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
