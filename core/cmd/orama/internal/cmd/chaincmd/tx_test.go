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
	baseFeeNorama string
	broadcastErr  error
	waitErr       error
	sent          int
}

func (f *fakeChain) Account(context.Context, string) (clusterreg.Account, error) {
	return clusterreg.Account{Number: 1, Sequence: 2}, nil
}
func (f *fakeChain) LatestHeight(context.Context) (uint64, error) { return 1000, nil }

func (f *fakeChain) BaseFee(context.Context) (string, error) {
	if f.baseFeeNorama != "" {
		return f.baseFeeNorama, nil
	}
	return "1", nil
}
func (f *fakeChain) SimulateGas(context.Context, []byte) (uint64, error) { return 50_000, nil }
func (f *fakeChain) Broadcast(_ context.Context, tx []byte) (string, error) {
	f.sent++
	return clusterreg.TxHash(tx), f.broadcastErr
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
	for _, want := range []string{"Public transfer on " + txChainID, "12.5 ORAMA (12500000000 norama)", txRecipient, onchain.PublicWarning,
		"fee     0.000", " norama)"} {
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

// The confirmation shows the fee that will be signed, before anything is signed.
func TestSend_publicConfirmationShowsTheFeeBeforeAnythingIsSigned(t *testing.T) {
	rig := newTxRig(t)
	_, errOut, err := runTx(t, "no\n", "send", txRecipient, "1", "--public")
	if err == nil {
		t.Fatal("a declined send succeeded")
	}
	// gas 50,000 x 1.5 = 75,000 at a base fee of 1 x 1.5 = 112,500 norama.
	if !strings.Contains(errOut, "112500 norama") || !strings.Contains(errOut, "0.0001125 ORAMA") {
		t.Errorf("the confirmation does not show the fee:\n%s", errOut)
	}
	if rig.wallet.signed != 0 || rig.chain.sent != 0 {
		t.Fatalf("a declined send signed %d and sent %d", rig.wallet.signed, rig.chain.sent)
	}
}

func TestSend_publicWithAFeeOverTheLimitSignsNothing(t *testing.T) {
	rig := newTxRig(t)
	rig.chain.baseFeeNorama = "100000000000"
	_, _, err := runTx(t, "", "send", txRecipient, "1", "--public", "--yes")
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("err = %v", err)
	}
	if rig.wallet.signed != 0 || rig.chain.sent != 0 {
		t.Fatalf("signed %d, sent %d", rig.wallet.signed, rig.chain.sent)
	}
}

func TestPinnedChainID(t *testing.T) {
	previous := expectedChainID
	t.Cleanup(func() { expectedChainID = previous })
	for name, tc := range map[string]struct {
		pinned, network, explicit string
		want                      string
		code                      int
	}{
		"the network names it": {"orama-stagenet-5", "stagenet", "", "orama-stagenet-5", 0},
		"the flag agrees":      {"orama-stagenet-5", "stagenet", "orama-stagenet-5", "orama-stagenet-5", 0},
		"the flag disagrees":   {"orama-stagenet-5", "stagenet", "orama-stagenet-6", "", clierr.CodeUsage},
		"the flag names it":    {"", "", "my-chain-1", "my-chain-1", 0},
		"nothing names it":     {"", "", "", "", clierr.CodeUsage},
	} {
		expectedChainID = func() (string, string, error) { return tc.pinned, tc.network, nil }
		got, err := pinnedChainID(tc.explicit)
		if got != tc.want || clierr.CodeOf(err) != tc.code {
			t.Errorf("%s: %q, %v (code %d)", name, got, err, clierr.CodeOf(err))
		}
	}
	expectedChainID = func() (string, string, error) { return "", "", errors.New("registry unreadable") }
	if _, err := pinnedChainID("x"); err == nil {
		t.Error("an unreadable registry was ignored")
	}
}

func TestRequireSecureEndpoint(t *testing.T) {
	for _, ok := range []string{"https://gw.example.com", "https://203.0.113.9:8443", "http://127.0.0.1:31003", "http://localhost:6001", "http://[::1]:8080"} {
		if err := requireSecureEndpoint(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://gw.example.com", "http://203.0.113.9:31003", "http://127.0.0.1.evil.example", "ftp://gw.example.com", "gw.example.com", "", "http://", "https://"} {
		if err := requireSecureEndpoint(bad); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

// realOpen runs the real openClient against a gateway that reports chain id served, with the network
// pinned to pinned, and counts the times the wallet was asked for.
func realOpen(t *testing.T, served, pinned, explicit, maxFee string) (txClient, error, int) {
	t.Helper()
	gw := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":{"node_info":{"network":"`+served+`"}}}`)
	})
	prevPin, prevSigner := expectedChainID, newSigner
	prevFlags, prevRead := txFlags, readFlags
	t.Cleanup(func() { expectedChainID, newSigner, txFlags, readFlags = prevPin, prevSigner, prevFlags, prevRead })
	expectedChainID = func() (string, string, error) {
		if pinned == "" {
			return "", "", nil
		}
		return pinned, "stagenet", nil
	}
	wallets := 0
	newSigner = func() onchain.Signer { wallets++; return &fakeWallet{} }
	readFlags.gateway, readFlags.node, readFlags.rpc = gw, "", ""
	txFlags.chainID, txFlags.maxFee = explicit, maxFee
	sendCmd.SetContext(context.Background())
	c, err := openClient(sendCmd)
	return c, err, wallets
}

func TestOpenClient_signsOnlyForTheChainTheNetworkNames(t *testing.T) {
	c, err, _ := realOpen(t, "orama-stagenet-5", "orama-stagenet-5", "", "")
	if err != nil || c.chainID != "orama-stagenet-5" {
		t.Fatalf("matching chain: %v, %v", c.chainID, err)
	}
	_, err, wallets := realOpen(t, "orama-evil-1", "orama-stagenet-5", "", "")
	if err == nil || !strings.Contains(err.Error(), "refusing to sign") {
		t.Fatalf("an endpoint on another chain: err = %v", err)
	}
	if wallets != 0 {
		t.Error("the wallet was opened for the wrong chain")
	}
	if _, err, _ = realOpen(t, "orama-stagenet-5", "", "", ""); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("a network that names no chain and no --chain-id: err = %v", err)
	}
	if _, err, _ = realOpen(t, "orama-x-1", "", "orama-x-1", ""); err != nil {
		t.Fatalf("an explicit --chain-id that matches: %v", err)
	}
	if _, err, _ = realOpen(t, "orama-x-1", "", "orama-y-1", ""); err == nil {
		t.Fatal("an explicit --chain-id that the endpoint contradicts was accepted")
	}
}

func TestOpenClient_aChainIDWithControlCharactersIsRefused(t *testing.T) {
	for _, served := range []string{"orama-1\u001b[2J", "orama 1", "orama-1\u202e", ""} {
		_, err, _ := realOpen(t, served, "orama-1", "", "")
		if err == nil {
			t.Errorf("chain id %q accepted", served)
			continue
		}
		if strings.ContainsAny(err.Error(), "\x1b\u202e") {
			t.Errorf("the error carries a control character: %q", err.Error())
		}
	}
}

func TestOpenClient_maxFeeIsInORAMAAndMustBePositive(t *testing.T) {
	if _, err, _ := realOpen(t, "orama-1", "orama-1", "", "0.5"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "-1", "abc", "1e3"} {
		if _, err, _ := realOpen(t, "orama-1", "orama-1", "", bad); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("--max-fee %q: err = %v", bad, err)
		}
	}
}
