package shielded

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// fakeBundle frames a canonical bundle (action count, actions, header, proof, signatures) whose
// i-th action carries cmxs[i]. Nothing in it is a valid proof; bundle.Parse only checks framing.
func fakeBundle(cmxs ...byte) []byte {
	n := len(cmxs)
	var b []byte
	b = append(b, byte(n))
	for i := 0; i < n; i++ {
		action := make([]byte, bundle.ActionLen)
		action[96] = cmxs[i] // cmx starts at offset 96
		b = append(b, action...)
	}
	header := make([]byte, bundle.HeaderLen)
	binary.LittleEndian.PutUint64(header[1:], 5)
	b = append(b, header...)
	proof := bundle.ProofLen(n)
	b = append(b, 0xfd, byte(proof), byte(proof>>8))
	b = append(b, make([]byte, proof+n*bundle.SigLen+bundle.SigLen)...)
	return b
}

type fakeSource map[int64][]TxRecord

func (f fakeSource) Txs(_ context.Context, h int64) ([]TxRecord, error) {
	recs, ok := f[h]
	if !ok {
		return nil, errors.New("block not served")
	}
	return recs, nil
}

func TestScanLeaves_appendsInBlockAndTransactionOrder(t *testing.T) {
	src := fakeSource{
		1: nil,
		2: {{Msgs: []sdk.Msg{&types.MsgShield{Bundle: fakeBundle(1, 2)}}}, {Msgs: []sdk.Msg{&types.MsgShieldedTransfer{Bundle: fakeBundle(3, 4)}}}},
		3: nil,
		4: {{Msgs: []sdk.Msg{&types.MsgUnshield{Bundle: fakeBundle(5, 6)}}}},
		5: nil,
	}
	got, err := ScanLeaves(context.Background(), src, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commitments) != 6 || got.LastChange != 4 {
		t.Fatalf("got %d leaves, last change %d", len(got.Commitments), got.LastChange)
	}
	for i, want := range []byte{1, 2, 3, 4, 5, 6} {
		if got.Commitments[i][:2] != hexByte(want) {
			t.Fatalf("leaf %d = %s, want first byte %02x", i, got.Commitments[i], want)
		}
	}
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0xf]})
}

func TestScanLeaves_skipsFailedTransactionsAndOtherMessages(t *testing.T) {
	src := fakeSource{
		1: {
			{Code: 5, Msgs: []sdk.Msg{&types.MsgShield{Bundle: fakeBundle(9, 9)}}},
			{Msgs: []sdk.Msg{&banktypes.MsgSend{}}},
		},
	}
	got, err := ScanLeaves(context.Background(), src, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commitments) != 0 || got.LastChange != 0 {
		t.Fatalf("a failed or unrelated transaction added leaves: %+v", got)
	}
}

func TestScanLeaves_errors(t *testing.T) {
	if _, err := ScanLeaves(context.Background(), fakeSource{}, 1, 1); err == nil {
		t.Fatal("a block the source could not serve was skipped")
	}
	bad := fakeSource{1: {{Msgs: []sdk.Msg{&types.MsgShield{Bundle: []byte{1, 2, 3}}}}}}
	if _, err := ScanLeaves(context.Background(), bad, 1, 1); err == nil {
		t.Fatal("an executed bundle that does not parse was skipped")
	}
}

func TestScanLeaves_emptyRange(t *testing.T) {
	got, err := ScanLeaves(context.Background(), fakeSource{}, 5, 4)
	if err != nil || len(got.Commitments) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}
