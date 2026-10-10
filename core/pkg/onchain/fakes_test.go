package onchain

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	testOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	testPubKey   = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
	testChainID  = "orama-teststage-1"
)

func testKey() []byte {
	k, _ := hex.DecodeString(testPubKey)
	return k
}

// fakeChain records what is sent to it and answers from its fields.
type fakeChain struct {
	account    clusterreg.Account
	accountErr error
	baseFee    string
	gasUsed    uint64
	simErr     error
	broadcast  error
	waitErr    error
	height     int64

	simulated [][]byte
	sent      [][]byte
}

func (f *fakeChain) Account(context.Context, string) (clusterreg.Account, error) {
	return f.account, f.accountErr
}
func (f *fakeChain) BaseFee(context.Context) (string, error) { return f.baseFee, nil }
func (f *fakeChain) SimulateGas(_ context.Context, tx []byte) (uint64, error) {
	f.simulated = append(f.simulated, tx)
	return f.gasUsed, f.simErr
}
func (f *fakeChain) Broadcast(_ context.Context, tx []byte) (string, error) {
	f.sent = append(f.sent, tx)
	return "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789", f.broadcast
}
func (f *fakeChain) WaitIncluded(context.Context, string) (int64, error) { return f.height, f.waitErr }

// fakeSigner is a RootWallet that signs with a fixed signature.
type fakeSigner struct {
	account    *rwagent.OramaAccount
	accountErr error
	signErr    error
	asAddress  string
	asKey      []byte

	accountCalls int
	signed       [][]byte
}

func newSigner() *fakeSigner {
	return &fakeSigner{account: &rwagent.OramaAccount{Address: testOperator, PubKey: testKey()}}
}

func (s *fakeSigner) OramaAccount(context.Context) (*rwagent.OramaAccount, error) {
	s.accountCalls++
	return s.account, s.accountErr
}

func (s *fakeSigner) SignOramaTx(_ context.Context, doc []byte) (*rwagent.OramaTxSignature, error) {
	s.signed = append(s.signed, doc)
	if s.signErr != nil {
		return nil, s.signErr
	}
	sig := &rwagent.OramaTxSignature{Signature: bytes.Repeat([]byte{9}, 64), PubKey: testKey(), Address: testOperator}
	if s.asAddress != "" {
		sig.Address = s.asAddress
	}
	if s.asKey != nil {
		sig.PubKey = s.asKey
	}
	return sig, nil
}

func newFakeChain() *fakeChain {
	return &fakeChain{account: clusterreg.Account{Number: 42, Sequence: 7}, baseFee: "10", gasUsed: 100_000, height: 55}
}

func newClient(t *testing.T, chain *fakeChain, signer *fakeSigner) *Client {
	t.Helper()
	c, err := New(chain, signer, testChainID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// signDocFields is what a test asserts on a SignDoc: the chain id, account
// number, sequence, gas limit and fee amount.
type signDocFields struct {
	chainID       string
	accountNumber uint64
	sequence      uint64
	gas           uint64
	fee           string
	typeURL       string
	pubKey        []byte
}

func decodeSignDoc(t *testing.T, doc []byte) signDocFields {
	t.Helper()
	var out signDocFields
	body, auth := field(t, doc, 1), field(t, doc, 2)
	out.chainID = string(field(t, doc, 3))
	out.accountNumber = varint(t, doc, 4)
	out.typeURL = string(field(t, field(t, body, 1), 1))
	signer := field(t, auth, 1)
	out.pubKey = field(t, field(t, field(t, signer, 1), 2), 1)
	out.sequence = varint(t, signer, 3)
	fee := field(t, auth, 2)
	out.gas = varint(t, fee, 2)
	out.fee = string(field(t, field(t, fee, 1), 2))
	return out
}

// field returns the first bytes field num of msg.
func field(t *testing.T, msg []byte, num protowire.Number) []byte {
	t.Helper()
	for len(msg) > 0 {
		n, typ, l := protowire.ConsumeTag(msg)
		if l < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(l))
		}
		msg = msg[l:]
		l = protowire.ConsumeFieldValue(n, typ, msg)
		if l < 0 {
			t.Fatalf("bad field %d: %v", n, protowire.ParseError(l))
		}
		if n == num && typ == protowire.BytesType {
			v, _ := protowire.ConsumeBytes(msg)
			return v
		}
		msg = msg[l:]
	}
	t.Fatalf("field %d not found", num)
	return nil
}

// varint returns the first varint field num of msg.
func varint(t *testing.T, msg []byte, num protowire.Number) uint64 {
	t.Helper()
	for len(msg) > 0 {
		n, typ, l := protowire.ConsumeTag(msg)
		msg = msg[l:]
		l = protowire.ConsumeFieldValue(n, typ, msg)
		if n == num && typ == protowire.VarintType {
			v, _ := protowire.ConsumeVarint(msg)
			return v
		}
		msg = msg[l:]
	}
	t.Fatalf("varint field %d not found", num)
	return 0
}
