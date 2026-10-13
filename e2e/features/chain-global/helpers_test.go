//go:build e2e_fleet

package chainglobal

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// signDocMarker is what every chain command prints before the hex sign
// document when it has no --node (core/cmd/orama/internal/cmd/globalcmd
// submit.go, clustercmd/register.go).
const signDocMarker = "sign document (not submitted):"

// Explicit fee and gas the documents carry: the commands have no default
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md: "The fee is an explicit amount of norama").
const (
	docFee = "1000"
	docGas = "200000"
)

// signDoc is a decoded cosmos.tx.v1beta1.SignDoc.
type signDoc struct {
	body, auth []byte
	chainID    string
	account    uint64
	typeURL    string
	msg        chain.Fields
	feeDenom   string
	feeAmount  string
	gas        uint64
	sequence   uint64
}

// Field numbers of SignDoc, TxBody, Any, AuthInfo, Fee, Coin, SignerInfo.
const (
	docBody, docAuth, docChainID, docAccount = 1, 2, 3, 4
	bodyMessages                             = 1
	anyType, anyValue                        = 1, 2
	authSigners, authFee                     = 1, 2
	feeAmount, feeGas                        = 1, 2
	coinDenom, coinAmount                    = 1, 2
	signerSequence                           = 3
)

// parseSignDoc reads the one-message sign document a command printed.
func parseSignDoc(t *testing.T, res oramacli.Result) signDoc {
	t.Helper()
	i := strings.Index(res.Stdout, signDocMarker)
	if i < 0 {
		t.Fatalf("orama %s printed no sign document:\n%s%s", strings.Join(oramacli.RedactArgs(res.Args), " "), res.Stdout, res.Stderr)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(res.Stdout[i+len(signDocMarker):]))
	if err != nil {
		t.Fatalf("sign document is not hex: %v", err)
	}
	f, err := chain.DecodePB(raw)
	if err != nil {
		t.Fatalf("sign document is not a protobuf SignDoc: %v", err)
	}
	if len(f[docBody]) != 1 || len(f[docAuth]) != 1 {
		t.Fatalf("sign document has %d bodies and %d auth infos", len(f[docBody]), len(f[docAuth]))
	}
	d := signDoc{chainID: f.Str(docChainID), account: f.Varint(docAccount)}
	d.body, _ = f[docBody][0].([]byte)
	d.auth, _ = f[docAuth][0].([]byte)
	body, err := chain.DecodePB(d.body)
	if err != nil || len(body[bodyMessages]) != 1 {
		t.Fatalf("sign document body: %v (%d messages)", err, len(body[bodyMessages]))
	}
	anyMsg, _ := body.Msg(bodyMessages)
	d.typeURL = anyMsg.Str(anyType)
	d.msg, _ = anyMsg.Msg(anyValue)
	auth, err := chain.DecodePB(d.auth)
	if err != nil {
		t.Fatalf("sign document auth info: %v", err)
	}
	fee, _ := auth.Msg(authFee)
	coin, _ := fee.Msg(feeAmount)
	d.feeDenom, d.feeAmount, d.gas = coin.Str(coinDenom), coin.Str(coinAmount), fee.Varint(feeGas)
	signer, _ := auth.Msg(authSigners)
	d.sequence = signer.Varint(signerSequence)
	return d
}

// requireDoc checks the parts every document shares.
func requireDoc(t *testing.T, name string, d signDoc, chainID string, account, sequence uint64, typeURL string) {
	t.Helper()
	if d.chainID != chainID || d.account != account || d.sequence != sequence || d.typeURL != typeURL {
		t.Errorf("%s: chain %q account %d sequence %d type %s; want %q %d %d %s", name, d.chainID, d.account, d.sequence, d.typeURL,
			chainID, account, sequence, typeURL)
	}
	if d.feeDenom != chain.Denom || d.feeAmount != docFee || fmt.Sprint(d.gas) != docGas {
		t.Errorf("%s: fee %s%s gas %d, want %s%s gas %s", name, d.feeAmount, d.feeDenom, d.gas, docFee, chain.Denom, docGas)
	}
}

// txRaw is the document's body and auth info as a TxRaw with no signature,
// the form `oramad tx decode` reads.
func (d signDoc) txRaw() []byte {
	return chain.PB{}.Bytes(1, d.body).Bytes(2, d.auth)
}

// signer is who a document is built for: a funded validator key, its
// account number and sequence, and its public key.
type signer struct {
	k        chain.Key
	account  uint64
	sequence uint64
	pubkey   string
}

func newSigner(t *testing.T, c *chain.Chain, i int) signer {
	t.Helper()
	k := c.FundedValidator(t, i, chain.Orama(1))
	acc, ok := c.AccountOf(t, k.Node, k.Address)
	if !ok {
		t.Fatalf("validator %s has no account", k.Address)
	}
	return signer{k: k, account: acc.Number, sequence: acc.Sequence, pubkey: c.PubKeyHex(t, k)}
}

// flags are the signing flags every chain command takes.
func (s signer) flags(c *chain.Chain) []string {
	return []string{"--chain-id", c.ID, "--fee", docFee, "--gas", docGas, "--pubkey", s.pubkey,
		"--account-number", fmt.Sprint(s.account), "--sequence", fmt.Sprint(s.sequence)}
}

// doc runs `orama <args> <signing flags>` without --node, requires exit 0,
// and returns the printed sign document.
func doc(t *testing.T, c *chain.Chain, s signer, args ...string) signDoc {
	t.Helper()
	res := infra.Run(t, harness.CLI(t), append(args, s.flags(c)...)...)
	infra.ExpectExit(t, res, infra.ExitOK, signDocMarker)
	return parseSignDoc(t, res)
}

// execute signs the document's body with the signer's own key on its node
// and delivers it: the chain decodes and runs exactly what the CLI built.
func execute(t *testing.T, c *chain.Chain, s signer, d signDoc) chain.Result {
	t.Helper()
	return c.SubmitUnsigned(t, s.k, chain.TxOptions{}, c.DecodeTxRaw(t, s.k.Node, d.txRaw()))
}
