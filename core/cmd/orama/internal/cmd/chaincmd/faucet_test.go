package chaincmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

const (
	faucetRecipient = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"
	faucetTxHash    = "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
)

func probeOut(chainID, signer string) string {
	return markStatus + `
{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"network":"` + chainID + `"}}}
` + markSigner + "\n" + signer + "\n"
}

func broadcastOut(code int, rawLog string) string {
	b, _ := json.Marshal(map[string]any{"code": code, "txhash": faucetTxHash, "raw_log": rawLog, "codespace": "emission"})
	return string(b) + "\n"
}

func resultOut(code int, rawLog, amount string) string {
	d, _ := json.Marshal(map[string]any{"code": code, "txhash": faucetTxHash, "raw_log": rawLog, "height": "77"})
	return markDelivered + "\n" + string(d) + "\n" + markBalance + "\n" +
		`{"balance":{"denom":"norama","amount":"` + amount + `"}}` + "\n"
}

// fakeNode stands in for the SSH path: the inventory, the key and the node's answers, one per script.
type fakeNode struct {
	answers []string
	errAt   int
	scripts []string
}

func (f *fakeNode) install(t *testing.T) {
	t.Helper()
	oldResolve, oldPrepare, oldRun := resolveNodes, prepareKeys, runRemote
	t.Cleanup(func() { resolveNodes, prepareKeys, runRemote = oldResolve, oldPrepare, oldRun })
	resolveNodes = func(env string) ([]inspector.Node, error) {
		return []inspector.Node{{Host: "203.0.113.1", User: "root"}, {Host: "203.0.113.2", User: "ubuntu"}}, nil
	}
	prepareKeys = func([]inspector.Node) (func(), error) { return func() {}, nil }
	runRemote = func(n inspector.Node, command string, _ ...remotessh.SSHOption) (string, error) {
		i := len(f.scripts)
		f.scripts = append(f.scripts, command)
		if f.errAt > 0 && i+1 == f.errAt {
			return "", errors.New("run on host: exit status 1 (stderr: boom)")
		}
		if i >= len(f.answers) {
			t.Fatalf("unexpected remote call %d: %s", i, command)
		}
		return f.answers[i], nil
	}
}

func runFaucetCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	for _, set := range []*pflag.FlagSet{Cmd.PersistentFlags(), faucetCmd.Flags()} {
		set.VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false })
	}
	var out bytes.Buffer
	Cmd.SetOut(&out)
	Cmd.SetErr(io.Discard)
	Cmd.SetArgs(append([]string{"faucet"}, args...))
	err := Cmd.Execute()
	return out.String(), err
}

func TestFaucetCmd_wiring(t *testing.T) {
	found, _, err := Cmd.Find([]string{"faucet"})
	if err != nil || found != faucetCmd {
		t.Fatalf("orama chain faucet is not wired: %v", err)
	}
	for name, def := range map[string]string{"env": "", "node": "", "amount": faucetDefaultAmount} {
		f := faucetCmd.Flags().Lookup(name)
		if f == nil || f.DefValue != def {
			t.Errorf("flag --%s = %+v, want default %q", name, f, def)
		}
	}
	if faucetCmd.Args == nil || faucetCmd.Args(faucetCmd, nil) == nil || faucetCmd.Args(faucetCmd, []string{"a", "b"}) == nil {
		t.Error("faucet must take exactly one recipient")
	}
}

func TestUnsignedFaucetTx_exactTypeURLAndFields(t *testing.T) {
	raw, err := unsignedFaucetTx(testAddr, faucetRecipient, big.NewInt(1500000000))
	if err != nil {
		t.Fatal(err)
	}
	var tx struct {
		Body struct {
			Messages []map[string]any `json:"messages"`
		} `json:"body"`
		AuthInfo struct {
			SignerInfos []any `json:"signer_infos"`
			Fee         struct {
				Amount   []any  `json:"amount"`
				GasLimit string `json:"gas_limit"`
			} `json:"fee"`
		} `json:"auth_info"`
		Signatures []any `json:"signatures"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.Body.Messages) != 1 {
		t.Fatalf("messages = %v, want exactly one", tx.Body.Messages)
	}
	want := map[string]any{"@type": "/orama.emission.v1.MsgFaucet", "signer": testAddr, "recipient": faucetRecipient, "amount": "1500000000"}
	got := tx.Body.Messages[0]
	if len(got) != len(want) {
		t.Errorf("message %v has fields beyond %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("message field %s = %v, want %v", k, got[k], v)
		}
	}
	if tx.AuthInfo.Fee.GasLimit != "300000" || len(tx.AuthInfo.Fee.Amount) != 0 || len(tx.Signatures) != 0 || len(tx.AuthInfo.SignerInfos) != 0 {
		t.Errorf("auth_info/signatures = %+v %v, want gas 300000, no fee amount, no signatures", tx.AuthInfo, tx.Signatures)
	}
}

func TestParseFaucetAmount(t *testing.T) {
	good := map[string]string{"1": "1", "100000000000": "100000000000", "999999999999999999": "999999999999999999"}
	for in, want := range good {
		got, err := parseFaucetAmount(in)
		if err != nil || got.String() != want {
			t.Errorf("parseFaucetAmount(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":                    "whole number",
		"0":                   "more than zero",
		"-5":                  "whole number",
		"1.5":                 "whole number",
		"1e9":                 "whole number",
		"abc":                 "whole number",
		"007":                 "whole number",
		" 5":                  "whole number",
		"1000000000000000000": "too large",
	}
	for in, frag := range bad {
		_, err := parseFaucetAmount(in)
		if err == nil || !strings.Contains(err.Error(), frag) || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("parseFaucetAmount(%q) error = %v, want a usage error mentioning %q", in, err, frag)
		}
	}
}

func TestRequireRecipient(t *testing.T) {
	if err := requireRecipient(faucetRecipient); err != nil {
		t.Fatalf("a valid address was refused: %v", err)
	}
	flipped := faucetRecipient[:len(faucetRecipient)-1] + "q"
	for name, addr := range map[string]string{
		"empty":        "",
		"wrong prefix": "cosmos1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53",
		"bad checksum": flipped,
		"upper case":   strings.ToUpper(faucetRecipient),
		"valoper":      "oramavaloper1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53",
		"truncated":    faucetRecipient[:20],
		"path":         "orama1abcdefgh/../x",
	} {
		err := requireRecipient(addr)
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: requireRecipient(%q) = %v, want a usage error", name, addr, err)
		}
	}
}

func TestRequireTestNetwork(t *testing.T) {
	for _, ok := range []string{"orama-stagenet-1", "orama-devnet-e2e-ab12cd", "orama-localnet-1"} {
		if err := requireTestNetwork(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"orama-1", "orama-mainnet-1", "", "stagenet", "orama-testnet-1"} {
		err := requireTestNetwork(bad)
		if err == nil || !strings.Contains(err.Error(), "not a test network") || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%q: error = %v, want the not-a-test-network usage error", bad, err)
		}
	}
}

func TestOrama(t *testing.T) {
	for in, want := range map[string]string{"0": "0", "1000000000": "1", "100000000000": "100", "1500000000": "1.5", "1": "0.000000001"} {
		n, _ := new(big.Int).SetString(in, 10)
		if got := orama(n); got != want {
			t.Errorf("orama(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestPickNode(t *testing.T) {
	nodes := []inspector.Node{{Host: "a"}, {Host: "b"}}
	if n, err := pickNode(nodes, "", "stagenet"); err != nil || n.Host != "a" {
		t.Errorf("default = %v, %v; want the first node", n, err)
	}
	if n, err := pickNode(nodes, "b", "stagenet"); err != nil || n.Host != "b" {
		t.Errorf("--node b = %v, %v", n, err)
	}
	if _, err := pickNode(nodes, "c", "stagenet"); clierr.CodeOf(err) != clierr.CodeNotFound {
		t.Errorf("an unknown node: %v", err)
	}
	if _, err := pickNode(nil, "", "stagenet"); clierr.CodeOf(err) != clierr.CodeNotFound {
		t.Errorf("no nodes: %v", err)
	}
}

func TestParseProbe_refusesWhatIsNotAChainStatus(t *testing.T) {
	if _, err := parseProbe(markStatus + "\n{}\n" + markSigner + "\n" + testAddr + "\n"); err == nil {
		t.Error("a status without a chain id was accepted")
	}
	if _, err := parseProbe(probeOut("orama-stagenet-1", "cosmos1xyz")); err == nil {
		t.Error("a signer that is not an orama account was accepted")
	}
	p, err := parseProbe(probeOut("orama-stagenet-1", testAddr))
	if err != nil || p.ChainID != "orama-stagenet-1" || p.Signer != testAddr {
		t.Errorf("probe = %+v, %v", p, err)
	}
}

func TestFaucet_dripsAndReports(t *testing.T) {
	f := &fakeNode{answers: []string{
		probeOut("orama-stagenet-1", testAddr),
		broadcastOut(0, ""),
		resultOut(0, "", "5000000000"),
	}}
	f.install(t)
	out, err := runFaucetCmd(t, faucetRecipient, "--env", "stagenet", "--amount", "5000000000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{faucetTxHash, "5000000000", faucetRecipient, "height 77", "orama-stagenet-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(f.scripts) != 3 {
		t.Fatalf("%d remote calls, want probe, sign+broadcast, result", len(f.scripts))
	}
	for _, s := range f.scripts {
		if strings.Contains(s, "keys export") || strings.Contains(s, "--unsafe") {
			t.Errorf("a remote script exports a key: %s", s)
		}
	}
	if !strings.Contains(f.scripts[1], "--from validator") || !strings.Contains(f.scripts[1], "--keyring-backend test") {
		t.Errorf("the transaction is not signed with the node's operator key:\n%s", f.scripts[1])
	}
	if !strings.Contains(f.scripts[0], "ip netns exec orama-global") {
		t.Errorf("the probe does not run in the chain's namespace:\n%s", f.scripts[0])
	}
}

// signedPayload digs the unsigned transaction out of the sign script's base64 argument.
func signedPayload(t *testing.T, script string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`printf %s '"'"'([A-Za-z0-9+/=]+)'"'"' \| base64 -d`).FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("no unsigned transaction in the script:\n%s", script)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatal(err)
	}
	var tx map[string]any
	if err := json.Unmarshal(raw, &tx); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestFaucet_sendsTheMessageTheNodeSigns(t *testing.T) {
	f := &fakeNode{answers: []string{probeOut("orama-devnet-1", testAddr), broadcastOut(0, ""), resultOut(0, "", "1")}}
	f.install(t)
	if _, err := runFaucetCmd(t, faucetRecipient, "--env", "devnet", "--amount", "1", "--node", "203.0.113.2"); err != nil {
		t.Fatal(err)
	}
	msg := signedPayload(t, f.scripts[1])["body"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	if msg["@type"] != faucetTypeURL || msg["signer"] != testAddr || msg["recipient"] != faucetRecipient || msg["amount"] != "1" {
		t.Errorf("the message the node signs is %v", msg)
	}
	if !strings.HasPrefix(f.scripts[0], "sudo bash -c ") {
		t.Errorf("a login that is not root must run under sudo: %.40s", f.scripts[0])
	}
}

func TestFaucet_refusesANonTestChainBeforeSigning(t *testing.T) {
	f := &fakeNode{answers: []string{probeOut("orama-1", testAddr)}}
	f.install(t)
	_, err := runFaucetCmd(t, faucetRecipient, "--env", "production")
	if err == nil || !strings.Contains(err.Error(), "not a test network") || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("error = %v, want the not-a-test-network refusal", err)
	}
	if len(f.scripts) != 1 {
		t.Errorf("%d remote calls: nothing but the probe may run on a non-test chain", len(f.scripts))
	}
}

func TestFaucet_refusalsFromTheChainCarryItsWords(t *testing.T) {
	cases := map[string][]string{
		"before a block": {probeOut("orama-stagenet-1", testAddr), broadcastOut(18, "faucet is not enabled"), ""},
		"in its block":   {probeOut("orama-stagenet-1", testAddr), broadcastOut(0, ""), resultOut(21, "recipient is in its 24h cooldown", "0")},
	}
	wants := map[string]string{"before a block": "faucet is not enabled", "in its block": "24h cooldown"}
	for name, answers := range cases {
		f := &fakeNode{answers: answers}
		f.install(t)
		_, err := runFaucetCmd(t, faucetRecipient, "--env", "stagenet")
		if err == nil || !strings.Contains(err.Error(), wants[name]) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error = %v, want the chain's refusal %q", name, err, wants[name])
		}
	}
}

func TestFaucet_remoteFailuresAreWrappedWithContext(t *testing.T) {
	for step := 1; step <= 3; step++ {
		f := &fakeNode{errAt: step, answers: []string{probeOut("orama-stagenet-1", testAddr), broadcastOut(0, ""), resultOut(0, "", "1")}}
		f.install(t)
		_, err := runFaucetCmd(t, faucetRecipient, "--env", "stagenet")
		if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "203.0.113.1") {
			t.Errorf("step %d: error = %v, want the host and the cause", step, err)
		}
	}
}

func TestFaucet_usageErrorsReachNoNode(t *testing.T) {
	f := &fakeNode{}
	f.install(t)
	for _, args := range [][]string{
		{"not-an-address", "--env", "stagenet"},
		{faucetRecipient, "--env", "stagenet", "--amount", "0"},
		{faucetRecipient, "--env", "stagenet", "--amount", "-1"},
		{faucetRecipient, "--env", "stagenet", "--node", "198.51.100.9"},
	} {
		if _, err := runFaucetCmd(t, args...); err == nil {
			t.Errorf("%v succeeded", args)
		}
	}
	if len(f.scripts) != 0 {
		t.Errorf("%d remote calls for invalid input", len(f.scripts))
	}
}

func TestParseBroadcast_edgeCases(t *testing.T) {
	if _, err := parseBroadcast("not json"); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := parseBroadcast(`{"code":0}`); err == nil {
		t.Error("an answer with no hash accepted")
	}
	if _, _, err := parseResult(markDelivered + "\n{}\n" + markBalance + "\n{\"balance\":{\"amount\":\"x\"}}\n"); err == nil {
		t.Error("a balance that is not a number accepted")
	}
}

// The scripts only run on a node, so at least their syntax is checked here.
func TestFaucetScripts_parseAsBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	for name, script := range map[string]string{
		"probe":  probeScript(),
		"sign":   faucetTxScript("orama-stagenet-1", []byte(`{"it's":"x"}`)),
		"result": resultScript(faucetTxHash, faucetRecipient),
	} {
		if out, err := exec.Command(bash, "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script does not parse: %v: %s", name, err, out)
		}
	}
}
