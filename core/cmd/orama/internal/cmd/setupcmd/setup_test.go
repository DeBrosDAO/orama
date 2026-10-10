package setupcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// resetFlags clears the package-level flags between tests.
func resetFlags(t *testing.T) {
	t.Helper()
	flags.network, flags.name, flags.user, flags.bootstrapKey, flags.domain = "", "", setup.DefaultSSHUser, "", ""
	flags.acmeCA, flags.env, flags.contact, flags.torNetwork = "", "", "", ""
	flags.ips, flags.hostKeys = nil, nil
	flags.clusterOnly, flags.exit, flags.yes, flags.password, flags.noValidator = false, false, false, false, false
	flags.allowQuorumLoss, flags.uploadRelease = false, false
	flags.storageGB, flags.asn = 0, 0
	flags.create = createFlags{}
	Cmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

func TestParseHostKeys(t *testing.T) {
	got, err := parseHostKeys([]string{"203.0.113.10=SHA256:aaa", "203.0.113.11=SHA256:bbb"})
	if err != nil || got["203.0.113.10"] != "SHA256:aaa" || got["203.0.113.11"] != "SHA256:bbb" {
		t.Fatalf("%v, %v", got, err)
	}
	bare, err := parseHostKeys([]string{"SHA256:aaa"})
	if err != nil || bare[""] != "SHA256:aaa" {
		t.Fatalf("%v, %v", bare, err)
	}
	if none, err := parseHostKeys(nil); err != nil || none != nil {
		t.Fatalf("%v, %v", none, err)
	}
}

func TestParseHostKeys_refusals(t *testing.T) {
	for name, in := range map[string][]string{
		"md5":               {"MD5:aa:bb"},
		"an empty digest":   {"SHA256:"},
		"a bare word":       {"fingerprint"},
		"an ip alone":       {"203.0.113.10="},
		"the same ip twice": {"203.0.113.10=SHA256:a", "203.0.113.10=SHA256:b"},
		"two bare ones":     {"SHA256:a", "SHA256:b"},
	} {
		_, err := parseHostKeys(in)
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: got %v, want a usage error", name, err)
		}
	}
}

func TestOptionsFromFlags(t *testing.T) {
	resetFlags(t)
	flags.network, flags.name, flags.yes, flags.storageGB = "stagenet", "alice", true, 80
	flags.ips = []string{"203.0.113.10"}
	flags.hostKeys = []string{"SHA256:aaa"}
	opts, err := optionsFromFlags(Cmd, []string{"203.0.113.11"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Network != "stagenet" || opts.Name != "alice" || !opts.Yes || opts.StorageGB != 80 || opts.User != "root" {
		t.Errorf("%+v", opts)
	}
	if strings.Join(opts.IPs, ",") != "203.0.113.10,203.0.113.11" {
		t.Errorf("the --ip flags and the arguments are both machines: %v", opts.IPs)
	}
	if opts.ASNSet {
		t.Error("--asn was not given")
	}
	if opts.UploadRelease {
		t.Error("machines download the release themselves unless --upload-release is given")
	}
}

func TestOptionsFromFlags_uploadReleaseIsAnExplicitChoice(t *testing.T) {
	resetFlags(t)
	flags.uploadRelease = true
	opts, err := optionsFromFlags(Cmd, nil)
	if err != nil || !opts.UploadRelease {
		t.Fatalf("%+v, %v", opts, err)
	}
}

func TestOptionsFromFlags_asnZeroIsAChoice(t *testing.T) {
	resetFlags(t)
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().Uint32("asn", 0, "")
	if err := cmd.Flags().Set("asn", "0"); err != nil {
		t.Fatal(err)
	}
	opts, err := optionsFromFlags(cmd, nil)
	if err != nil || !opts.ASNSet || opts.ASN != 0 {
		t.Fatalf("%+v, %v: --asn 0 leaves the ASN undeclared on purpose", opts, err)
	}
}

func TestWantWizard(t *testing.T) {
	full := setup.Options{IPs: []string{"203.0.113.10"}, Name: "alice"}
	cases := []struct {
		name string
		opts setup.Options
		tty  bool
		want bool
	}{
		{"no flags on a terminal", setup.Options{}, true, true},
		{"everything given", full, true, false},
		{"no name for a full node", setup.Options{IPs: full.IPs}, true, true},
		{"no name needed cluster-only", setup.Options{IPs: full.IPs, ClusterOnly: true}, true, false},
		{"--yes asks nothing", setup.Options{Yes: true}, true, false},
		{"a creation needs no node name", setup.Options{IPs: full.IPs, Create: &setup.CreateOptions{}}, true, false},
		{"a creation still needs machines", setup.Options{Create: &setup.CreateOptions{}}, true, true},
		{"no terminal, no questions", setup.Options{}, false, false},
	}
	for _, c := range cases {
		if got := wantWizard(c.opts, c.tty); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRequireHostKeys(t *testing.T) {
	two := setup.Options{IPs: []string{"203.0.113.10", "203.0.113.11"}, Name: "alice", Yes: true, HostKeys: map[string]string{"203.0.113.10": "SHA256:a"}}
	err := requireHostKeys(two, false)
	if err == nil || !strings.Contains(err.Error(), "203.0.113.11") || !strings.Contains(err.Error(), "--host-key 203.0.113.11=SHA256:...") {
		t.Fatalf("got %v, want the machine without a fingerprint named", err)
	}
	two.HostKeys["203.0.113.11"] = "SHA256:b"
	if err := requireHostKeys(two, false); err != nil {
		t.Fatal(err)
	}
	single := setup.Options{IPs: []string{"203.0.113.10"}, Name: "alice", Yes: true, HostKeys: map[string]string{"": "SHA256:a"}}
	if err := requireHostKeys(single, false); err != nil {
		t.Fatalf("a bare fingerprint pins the one machine: %v", err)
	}
	if err := requireHostKeys(setup.Options{IPs: []string{"203.0.113.10"}, Name: "alice"}, true); err != nil {
		t.Fatalf("on a terminal without --yes the host key is confirmed interactively: %v", err)
	}
	noTTY := setup.Options{IPs: []string{"203.0.113.10"}, Name: "alice"}
	if err := requireHostKeys(noTTY, false); err == nil || !strings.Contains(err.Error(), "SHA256:...") {
		t.Fatalf("without a terminal there is nobody to ask: %v", err)
	}
}

func TestRequireHostKeys_badOptionsAreUsageErrors(t *testing.T) {
	if err := requireHostKeys(setup.Options{Yes: true}, false); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("got %v", err)
	}
}

func TestAskYesNo(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, " yes \n": true, "n\n": false, "\n": false, "maybe\n": false} {
		var out bytes.Buffer
		got, err := askYesNo(strings.NewReader(in), &out, "Go ahead?")
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", in, got, err, want)
		}
		if !strings.Contains(out.String(), "Go ahead? [y/N]") {
			t.Errorf("prompt %q", out.String())
		}
	}
	if _, err := askYesNo(strings.NewReader(""), &bytes.Buffer{}, "Go ahead?"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("no answer to read: %v", err)
	}
}

func TestConfirmExit(t *testing.T) {
	var out bytes.Buffer
	opts := setup.Options{Exit: true}
	if err := confirmExit(strings.NewReader("yes\n"), &out, &opts); err != nil || !opts.ExitConfirmed {
		t.Fatalf("%v %+v", err, opts)
	}
	if !strings.Contains(out.String(), "abuse complaints") {
		t.Errorf("the warning is shown first: %q", out.String())
	}
	declined := setup.Options{Exit: true}
	err := confirmExit(strings.NewReader("no\n"), &bytes.Buffer{}, &declined)
	if clierr.CodeOf(err) != clierr.CodeAborted || declined.ExitConfirmed {
		t.Fatalf("declining the exit is an abort: %v", err)
	}
}

func TestChoicesFrom_marksTheActiveNetworkTheDefault(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, name := range []string{"stagenet", "testnet"} {
		root := []byte("root-" + name)
		m := netregistry.Manifest{Name: name, ChainID: "orama-" + name + "-1", GenesisSHA256: strings.Repeat("0", 64),
			Seeds: []string{"a." + name + ".example", "b." + name + ".example"}, Channel: "nightly", MinVersion: "0.3.0",
			ReleaseRepo: "https://r.example", ReleaseRootSHA256: netregistry.Digest(root)}
		data, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		fsys["r/"+name+"/manifest.json"] = &fstest.MapFile{Data: data}
		fsys["r/"+name+"/release-root.json"] = &fstest.MapFile{Data: root}
	}
	reg, err := netregistry.LoadFS(fsys, "r")
	if err != nil {
		t.Fatal(err)
	}
	got, err := choicesFrom(reg, "testnet")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if got[0].Name != "stagenet" || got[0].Default || got[1].Name != "testnet" || !got[1].Default || got[1].ChainID != "orama-testnet-1" {
		t.Errorf("%+v", got)
	}
}

func TestChoiceOf_saysWhetherTheNetworkPinsATorNetwork(t *testing.T) {
	plain := &netregistry.Network{Manifest: &netregistry.Manifest{Name: "stagenet", ChainID: "orama-stagenet-1"}}
	pinned := &netregistry.Network{Manifest: &netregistry.Manifest{Name: "testnet", ChainID: "orama-testnet-1", TorNetworkSHA256: strings.Repeat("ab", 32)}}
	if c := choiceOf(plain, "stagenet"); c.TorNetwork || !c.Default || c.Name != "stagenet" {
		t.Errorf("%+v", c)
	}
	if c := choiceOf(pinned, "stagenet"); !c.TorNetwork || c.Default {
		t.Errorf("%+v", c)
	}
}

func TestPrintSummary(t *testing.T) {
	var out bytes.Buffer
	plan := &setup.Plan{Nodes: []setup.NodePlan{{IP: "203.0.113.10"}, {IP: "203.0.113.11"}}}
	printSummary(&out, &setup.Result{Plan: plan, Env: "stagenet-alice", Operator: "orama1abc"})
	for _, want := range []string{`recorded as "stagenet-alice"`, "203.0.113.10", "203.0.113.11", "Operator account: orama1abc", "orama status --env stagenet-alice"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
}

func TestCleanError_keepsTheCodeAndDropsTheEscapes(t *testing.T) {
	err := cleanError(clierr.Conflict("the chain says \x1b]52;c;ZXZpbA==\x07 no"))
	if clierr.CodeOf(err) != clierr.CodeConflict {
		t.Errorf("code %d, want the conflict code kept", clierr.CodeOf(err))
	}
	if strings.ContainsAny(err.Error(), "\x1b\x07") || !strings.Contains(err.Error(), "the chain says") {
		t.Errorf("message %q", err.Error())
	}
	if cleanError(nil) != nil {
		t.Error("no error stays no error")
	}
	plain := cleanError(errors.New("bad\x1b[2J"))
	if clierr.CodeOf(plain) != clierr.CodeFailure || strings.ContainsRune(plain.Error(), '\x1b') {
		t.Errorf("%v (code %d)", plain, clierr.CodeOf(plain))
	}
}

func TestCmd_isRegisteredWithItsFlags(t *testing.T) {
	for _, name := range []string{"network", "ip", "name", "cluster-only", "exit", "storage-gb", "yes", "user", "password", "bootstrap-key", "host-key", "domain", "env", "tor-network", "asn", "no-validator", "contact", "acme-ca",
		"create-network", "chain-id", "release-root", "release-repo", "channel", "min-version", "seed", "publish-dir", "force-new-genesis", "no-faucet", "upload-release"} {
		if Cmd.Flags().Lookup(name) == nil {
			t.Errorf("orama setup has no --%s", name)
		}
	}
	if Cmd.Flags().ShorthandLookup("y") == nil {
		t.Error("-y is --yes")
	}
}

func TestFilterStreams_whatReachesTheRealStreamsIsCleaned(t *testing.T) {
	realR, realW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = realW, realW
	restore, err := filterStreams()
	if err != nil {
		os.Stdout, os.Stderr = oldOut, oldErr
		t.Fatal(err)
	}
	fmt.Fprint(os.Stdout, "Continue? [y/N]: ")
	fmt.Fprint(os.Stderr, "\x1b]0;owned\x07 from a machine")
	restore()
	if os.Stdout != realW || os.Stderr != realW {
		t.Fatal("the streams are not put back")
	}
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = realW.Close()
	raw, err := io.ReadAll(realR)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.ContainsAny(got, "\x1b\x07") || !strings.Contains(got, "Continue? [y/N]: ") || !strings.Contains(got, "from a machine") {
		t.Errorf("got %q", got)
	}
}

func TestCleanError_theOriginalErrorStaysReachable(t *testing.T) {
	root := errors.New("rqlite is down")
	err := cleanError(fmt.Errorf("machine x: %w", root))
	if !errors.Is(err, root) {
		t.Error("errors.Is must still reach the cause through the cleaned error")
	}
	var nf interface{ Error() string }
	if !errors.As(err, &nf) {
		t.Error("errors.As must work")
	}
}

func TestOptionsFromFlags_createNetwork(t *testing.T) {
	resetFlags(t)
	for name, value := range map[string]string{
		"create-network": "stagenet", "chain-id": "orama-stagenet-7", "release-root": "root.json", "publish-dir": "out",
		"seed": "seed.example.org", "no-faucet": "true", "force-new-genesis": "true", "ip": "203.0.113.10",
	} {
		if err := Cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := optionsFromFlags(Cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := setup.CreateOptions{
		Name: "stagenet", ChainID: "orama-stagenet-7", ReleaseRoot: "root.json", PublishDir: "out",
		Seeds: []string{"seed.example.org"}, NoFaucet: true, ForceNewGenesis: true,
	}
	if opts.Create == nil || fmt.Sprint(*opts.Create) != fmt.Sprint(want) {
		t.Errorf("create = %+v, want %+v", opts.Create, want)
	}
	resetFlags(t)
}

func TestOptionsFromFlags_aCreationFlagWithoutCreateNetworkIsRefused(t *testing.T) {
	for _, name := range createOnly {
		resetFlags(t)
		value := "x"
		if name == "force-new-genesis" || name == "no-faucet" {
			value = "true"
		}
		if err := Cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
		_, err := optionsFromFlags(Cmd, nil)
		if clierr.CodeOf(err) != clierr.CodeUsage || err == nil || !strings.Contains(err.Error(), "--"+name) {
			t.Errorf("--%s without --create-network: %v", name, err)
		}
	}
	resetFlags(t)
}

func TestPrintSummary_aCreationSaysHowToPublish(t *testing.T) {
	var out bytes.Buffer
	plan := &setup.Plan{Nodes: []setup.NodePlan{{IP: "203.0.113.10"}}}
	created := &setup.CreatedNetwork{
		Manifest: &netregistry.Manifest{Name: "stagenet", ChainID: "orama-stagenet-7", Seeds: []string{"ns1.stagenet.orama.network"}},
		Dir:      "networks/stagenet", Machines: []string{"203.0.113.10"},
	}
	printSummary(&out, &setup.Result{Plan: plan, Env: "stagenet", Created: created})
	for _, want := range []string{"make -C core sync-networks", "orama network add https://orama.network/networks/stagenet/manifest.json", "ns1.stagenet.orama.network."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
}

func TestChoicesFrom_marksAnAnnouncedNetwork(t *testing.T) {
	root := []byte("root-newnet")
	m := netregistry.Manifest{Name: "newnet", ChainID: "orama-newnet-stagenet-1", Channel: "nightly", MinVersion: "0.3.0",
		ReleaseRepo: "https://r.example", ReleaseRootSHA256: netregistry.Digest(root)}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := netregistry.LoadFS(fstest.MapFS{
		"r/newnet/manifest.json": &fstest.MapFile{Data: data}, "r/newnet/release-root.json": &fstest.MapFile{Data: root},
	}, "r")
	if err != nil {
		t.Fatal(err)
	}
	got, err := choicesFrom(reg, "")
	if err != nil || len(got) != 1 || !got[0].Announced {
		t.Fatalf("%+v, %v", got, err)
	}
}

// An announced network supplies the chain id and the release root of a creation; the host-key check
// runs before that is resolved, so it must not check the creation itself. It used to, and refused
// `orama setup --create-network stagenet --yes ...` with "needs --chain-id".
func TestRequireHostKeys_anAnnouncedCreationIsNotCheckedYet(t *testing.T) {
	create := setup.Options{IPs: []string{"203.0.113.10"}, Yes: true, HostKeys: map[string]string{"": "SHA256:a"},
		Create: &setup.CreateOptions{Name: "stagenet"}}
	if err := requireHostKeys(create, false); err != nil {
		t.Fatalf("a creation whose chain id the announcement supplies was refused: %v", err)
	}
}
