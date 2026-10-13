package onchain

import (
	"bytes"
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func relayFor(node string) clusterreg.RelayRegistration {
	return clusterreg.RelayRegistration{
		NodeID:           node,
		RSAFingerprint:   bytes.Repeat([]byte{0x11}, clusterreg.RelayFingerprintLen),
		Exit:             true,
		Ed25519Signature: bytes.Repeat([]byte{0x22}, clusterreg.RelaySignatureLen),
		RSAPublicKey:     bytes.Repeat([]byte{0x33}, 9),
		RSASignature:     bytes.Repeat([]byte{0x44}, 16),
	}
}

func TestRegisterRelay_sendsMsgRegisterRelayForTheSigningOperator(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	receipt, err := newClient(t, chain, signer).RegisterRelay(context.Background(), relayFor("node-a"))
	if err != nil {
		t.Fatalf("RegisterRelay: %v", err)
	}
	if receipt == nil || receipt.Hash == "" {
		t.Errorf("receipt = %+v", receipt)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.RegisterRelayTypeURL {
		t.Errorf("type = %s, want MsgRegisterRelay", got)
	}
	for _, want := range []string{testOperator, "node-a"} {
		if !bytes.Contains(chain.sent[0], []byte(want)) {
			t.Errorf("the transaction does not carry %q", want)
		}
	}
	if !bytes.Contains(chain.sent[0], bytes.Repeat([]byte{0x11}, clusterreg.RelayFingerprintLen)) {
		t.Error("the transaction does not carry the fingerprint")
	}
}

func TestUpdateRelay_sendsMsgUpdateRelay(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	if _, err := newClient(t, chain, signer).UpdateRelay(context.Background(), relayFor("node-a")); err != nil {
		t.Fatalf("UpdateRelay: %v", err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.UpdateRelayTypeURL {
		t.Errorf("type = %s, want MsgUpdateRelay", got)
	}
}

func TestRegisterRelay_refusesABadRelayBeforeTheChainIsAsked(t *testing.T) {
	short := relayFor("node-a")
	short.RSAFingerprint = short.RSAFingerprint[:19]
	unsigned := relayFor("node-a")
	unsigned.Ed25519Signature = nil
	noProof := relayFor("node-a")
	noProof.RSASignature = nil
	for name, r := range map[string]clusterreg.RelayRegistration{
		"no node id":          relayFor(""),
		"a bad node id":       relayFor("bad id"),
		"a short fingerprint": short,
		"no signature":        unsigned,
		"no rsa proof":        noProof,
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).RegisterRelay(context.Background(), r); err == nil {
				t.Fatal("the relay was accepted")
			}
			if len(chain.simulated) != 0 {
				t.Error("the relay reached the chain")
			}
		})
	}
}

func TestRegisterRelay_aRefusedBlockIsAnError(t *testing.T) {
	chain := newFakeChain()
	chain.waitErr = context.DeadlineExceeded
	if _, err := newClient(t, chain, newSigner()).RegisterRelay(context.Background(), relayFor("node-a")); err == nil {
		t.Fatal("a registration the block refused was reported as made")
	}
}
