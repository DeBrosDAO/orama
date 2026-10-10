package clusterreg

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// fields returns the fields of one protobuf message in order: number, wire type and,
// for a varint, its value; for bytes, its content.
type field struct {
	num   protowire.Number
	typ   protowire.Type
	value uint64
	bytes []byte
}

func parseFields(t *testing.T, b []byte) []field {
	t.Helper()
	var out []field
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		f := field{num: num, typ: typ}
		switch typ {
		case protowire.VarintType:
			f.value, n = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			f.bytes, n = protowire.ConsumeBytes(b)
		default:
			t.Fatalf("unexpected wire type %d", typ)
		}
		if n < 0 {
			t.Fatalf("bad field %d: %v", num, protowire.ParseError(n))
		}
		b = b[n:]
		out = append(out, f)
	}
	return out
}

func get(fs []field, num protowire.Number) (field, bool) {
	for _, f := range fs {
		if f.num == num {
			return f, true
		}
	}
	return field{}, false
}

func canonicalTestDoc(sequence, account uint64) Direct {
	pub := append([]byte{0x02}, bytes.Repeat([]byte{0x11}, 32)...)
	return Direct{
		TypeURL: RegisterClusterTypeURL, Msg: []byte{0x0a, 0x01, 'x'}, PubKey: pub,
		Sequence: sequence, FeeAmount: "1000", Gas: 200000, ChainID: "orama-stagenet-6", AccountNumber: account,
	}
}

// The stagenet create run of 2026-10-10: the operator's first transaction has sequence 0,
// and the AuthInfo carried "sequence: 0", which proto3 never writes; the RootWallet agent
// refused to sign bytes that are not canonical. A zero account number has the same flaw in
// the SignDoc, where the chain would verify other bytes than were signed.
func TestSignDoc_zeroSequenceAndAccountAreNotWritten(t *testing.T) {
	doc, err := canonicalTestDoc(0, 0).SignDoc()
	if err != nil {
		t.Fatal(err)
	}
	top := parseFields(t, doc)
	if _, ok := get(top, 4); ok {
		t.Error("the SignDoc writes account_number 0")
	}
	auth, ok := get(top, 2)
	if !ok {
		t.Fatal("no auth_info")
	}
	signer, ok := get(parseFields(t, auth.bytes), 1)
	if !ok {
		t.Fatal("no signer_info")
	}
	if _, ok := get(parseFields(t, signer.bytes), 3); ok {
		t.Error("the signer info writes sequence 0")
	}
}

func TestSignDoc_nonZeroSequenceAndAccountAreWritten(t *testing.T) {
	doc, err := canonicalTestDoc(7, 42).SignDoc()
	if err != nil {
		t.Fatal(err)
	}
	top := parseFields(t, doc)
	if f, ok := get(top, 4); !ok || f.value != 42 {
		t.Errorf("account_number = %+v, want 42", f)
	}
	auth, _ := get(top, 2)
	signer, _ := get(parseFields(t, auth.bytes), 1)
	if f, ok := get(parseFields(t, signer.bytes), 3); !ok || f.value != 7 {
		t.Errorf("sequence = %+v, want 7", f)
	}
}

// The transaction carries the same auth info bytes as the document that was signed.
func TestTxRaw_carriesTheSignedAuthInfo(t *testing.T) {
	d := canonicalTestDoc(0, 0)
	doc, _ := d.SignDoc()
	raw, err := d.TxRaw(bytes.Repeat([]byte{1}, 64))
	if err != nil {
		t.Fatal(err)
	}
	docAuth, _ := get(parseFields(t, doc), 2)
	txAuth, _ := get(parseFields(t, raw), 2)
	if !bytes.Equal(docAuth.bytes, txAuth.bytes) {
		t.Error("the transaction's auth info differs from the signed one")
	}
}
