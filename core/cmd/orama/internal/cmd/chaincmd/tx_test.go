package chaincmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	txRecipient = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"
	txChainID   = "orama-teststage-1"
	txPubKey    = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
)

type fakeChain struct {
	broadcastErr error
	waitErr      error
	sent         int
}

func (f *fakeChain) Account(context.Context, string) (clusterreg.Account, error) {
	return clusterreg.Account{Number: 1, Sequence: 2}, nil
}
func (f *fakeChain) BaseFee(context.Context) (string, error)             { return "1", nil }
func (f *fakeChain) SimulateGas(context.Context, []byte) (uint64, error) { return 50_000, nil }
func (f *fakeChain) Broadcast(context.Context, []byte) (string, error) {
	f.sent++
	return strings.Repeat("AB", 32), f.broadcastErr
}
func (f *fakeChain) WaitIncluded(context.Context, string) (int64, error) { return 77, f.waitErr }

type fakeWallet struct {
	signErr error
	signed  int
}

func (w *fakeWallet) OramaAccount(context.Context) (*rwagent.OramaAccount, error) {
	return &rwagent.OramaAccount{Address: testAddr, PubKey: pubKey()}, nil
}

func (w *fakeWallet) SignOramaTx(context.Context, []byte) (*rwagent.OramaTxSignature, error) {
	w.signed++
	if w.signErr != nil {
		return nil, w.signErr
	}
	return &rwagent.OramaTxSignature{Signature: bytes.Repeat([]byte{9}, 64), PubKey: pubKey(), Address: testAddr}, nil
}

func pubKey() []byte {
	b, _ := hex.DecodeString(txPubKey)
	return b
}

// txRig replaces openClient with a client over a fake chain and a fake wallet, and counts how many
// times a command opened one.
type txRig struct {
	chain  *fakeChain
	wallet *fakeWallet
	opened int
}

func newTxRig(t *testing.T) *txRig {
	t.Helper()
	rig := &txRig{chain: &fakeChain{}, wallet: &fakeWallet{}}
	previous := openClient
	t.Cleanup(func() { openClient = previous })
	openClient = func(*cobra.Command) (txClient, error) {
		rig.opened++
		c, err := onchain.New(rig.chain, rig.wallet, txChainID)
		if err != nil {
			t.Fatal(err)
		}
		return txClient{Client: c, chainID: txChainID}, nil
	}
	return rig
}

// runTx runs `orama chain <args>` with stdin and every flag of the transaction commands reset.
func runTx(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	if Cmd.PersistentFlags().Lookup(printer.JSONFlag) == nil {
		printer.Register(Cmd)
	}
	for _, set := range []*pflag.FlagSet{Cmd.PersistentFlags(), sendCmd.Flags(), withdrawCmd.Flags()} {
		set.VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false })
	}
	t.Setenv("ORAMA_GATEWAY_URL", "")
	var out, errw bytes.Buffer
	Cmd.SetOut(&out)
	Cmd.SetErr(&errw)
	Cmd.SetIn(strings.NewReader(stdin))
	Cmd.SetArgs(args)
	defer Cmd.SetIn(nil)
	err = Cmd.Execute()
	return out.String(), errw.String(), err
}

func TestSend_withoutPublicIsPrivateAndNeverReachesTheWalletOrTheChain(t *testing.T) {
	rig := newTxRig(t)
	_, _, err := runTx(t, "", "send", txRecipient, "1")
	if err == nil || !strings.Contains(err.Error(), "a private transfer needs the RootWallet") {
		t.Fatalf("err = %v, want the private transfer to be unavailable", err)
	}
	if !strings.Contains(err.Error(), "choose a public transfer explicitly") {
		t.Errorf("the error does not say how to pay publicly: %v", err)
	}
	if rig.opened != 0 || rig.wallet.signed != 0 || rig.chain.sent != 0 {
		t.Fatalf("a private send opened %d clients, signed %d, sent %d", rig.opened, rig.wallet.signed, rig.chain.sent)
	}
}

func TestSend_publicWithYesSendsAndWarns(t *testing.T) {
	rig := newTxRig(t)
	out, errOut, err := runTx(t, "", "send", txRecipient, "12.5", "--public", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if rig.wallet.signed != 1 || rig.chain.sent != 1 {
		t.Fatalf("signed %d, sent %d", rig.wallet.signed, rig.chain.sent)
	}
	for _, want := range []string{"Public transfer on " + txChainID, "12.5 ORAMA (12500000000 norama)", txRecipient, onchain.PublicWarning} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, errOut)
		}
	}
	if !strings.Contains(out, "block 77") {
		t.Errorf("the result lacks the block: %q", out)
	}
}

func TestSend_publicAsksAndOnlyYesSends(t *testing.T) {
	for stdin, sends := range map[string]bool{"yes\n": true, "y\n": false, "no\n": false, "": false, "YES\n": false} {
		rig := newTxRig(t)
		_, _, err := runTx(t, stdin, "send", txRecipient, "1", "--public")
		if sends && err != nil {
			t.Errorf("stdin %q: %v", stdin, err)
		}
		if !sends && (err == nil || clierr.CodeOf(err) != clierr.CodeAborted) {
			t.Errorf("stdin %q: err = %v, want an aborted confirmation", stdin, err)
		}
		if got := rig.chain.sent == 1; got != sends {
			t.Errorf("stdin %q: sent = %d", stdin, rig.chain.sent)
		}
	}
}

func TestSend_publicRefusalsAreUsageErrorsBeforeAnyWalletOrChain(t *testing.T) {
	for name, args := range map[string][]string{
		"recipient is not an address": {"send", "bob", "1", "--public"},
		"a valoper recipient":         {"send", "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0al2xuls", "1", "--public"},
		"zero":                        {"send", txRecipient, "0", "--public"},
		"norama written as ORAMA":     {"send", txRecipient, "1e9", "--public"},
		"too many decimals":           {"send", txRecipient, "0.0000000001", "--public"},
	} {
		rig := newTxRig(t)
		_, _, err := runTx(t, "yes\n", args...)
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: err = %v code %d, want usage", name, err, clierr.CodeOf(err))
		}
		if rig.opened != 0 {
			t.Errorf("%s: opened a client", name)
		}
	}
}

func TestSend_publicRefusedByTheWalletIsAnAuthError(t *testing.T) {
	rig := newTxRig(t)
	rig.wallet.signErr = &rwagent.AgentError{Code: rwagent.CodeApprovalDenied, Message: "denied", StatusCode: http.StatusForbidden}
	_, _, err := runTx(t, "", "send", txRecipient, "1", "--public", "--yes")
	if err == nil || clierr.CodeOf(err) != clierr.CodeAuth {
		t.Fatalf("err = %v code %d, want an auth error", err, clierr.CodeOf(err))
	}
	if rig.chain.sent != 0 {
		t.Fatal("a refused transaction was broadcast")
	}
}

func TestSend_publicJSONCarriesThePrivacyAndTheWarning(t *testing.T) {
	newTxRig(t)
	out, _, err := runTx(t, "", "send", txRecipient, "1", "--public", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got sendReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %q: %v", out, err)
	}
	if got.Privacy != "public" || got.Warning != onchain.PublicWarning || got.Amount != "1000000000" || got.To != txRecipient || got.Height != 77 {
		t.Errorf("report = %+v", got)
	}
}

func TestSend_rpcIsNotATransactionRoute(t *testing.T) {
	previous := openClient
	t.Cleanup(func() { openClient = previous })
	_, _, err := runTx(t, "", "send", txRecipient, "1", "--public", "--yes", "--rpc", "http://127.0.0.1:1")
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestWithdrawEarnings_withdrawsToTheSigner(t *testing.T) {
	rig := newTxRig(t)
	out, _, err := runTx(t, "", "withdraw-earnings", "25")
	if err != nil {
		t.Fatal(err)
	}
	if rig.chain.sent != 1 || !strings.Contains(out, "withdrew 25 ORAMA") || !strings.Contains(out, testAddr) {
		t.Fatalf("sent %d out %q", rig.chain.sent, out)
	}
}

func TestWithdrawEarnings_refusesBadAmountsBeforeTheWallet(t *testing.T) {
	for _, amount := range []string{"0", "-5", "all", "1.0000000001", ""} {
		rig := newTxRig(t)
		_, _, err := runTx(t, "", "withdraw-earnings", amount)
		if err == nil || rig.opened != 0 {
			t.Errorf("amount %q: err = %v, opened %d", amount, err, rig.opened)
		}
	}
}

func TestWithdrawEarnings_aChainRefusalIsAFailure(t *testing.T) {
	rig := newTxRig(t)
	rig.chain.waitErr = errors.New("the transaction failed in block 3 (code 9): insufficient earnings")
	_, _, err := runTx(t, "", "withdraw-earnings", "1")
	if err == nil || clierr.CodeOf(err) != clierr.CodeFailure || !strings.Contains(err.Error(), "insufficient earnings") {
		t.Fatalf("err = %v code %d", err, clierr.CodeOf(err))
	}
}

func TestChainID_readsTheGatewayStatusAndANodesInfo(t *testing.T) {
	gw := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":{"node_info":{"network":"orama-stagenet-5"}}}`)
	})
	got, err := chainID(context.Background(), &chainread.Reader{Gateway: gw})
	if err != nil || got != "orama-stagenet-5" {
		t.Fatalf("gateway: %q, %v", got, err)
	}
	node := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != nodeInfoPath {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"default_node_info":{"network":"orama-stagenet-6"}}`)
	})
	got, err = chainID(context.Background(), &chainread.Reader{REST: node})
	if err != nil || got != "orama-stagenet-6" {
		t.Fatalf("node: %q, %v", got, err)
	}
}

func TestChainID_refusesAStatusWithoutOne(t *testing.T) {
	gw := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"result":{"node_info":{}}}`) })
	if _, err := chainID(context.Background(), &chainread.Reader{Gateway: gw}); err == nil {
		t.Fatal("a status with no chain id was accepted")
	}
	down := server(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", http.StatusBadGateway) })
	_, err := chainID(context.Background(), &chainread.Reader{Gateway: down})
	if err == nil || clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}
