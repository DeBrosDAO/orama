package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/invite"
)

// recShell records the commands a machine runs and answers from a map keyed by a
// fragment of the command.
type recShell struct {
	calls   []string
	stdins  map[string]string
	answers map[string]string
	fail    map[string]error
	uploads []string
}

func (r *recShell) Run(_ context.Context, command string, stdin io.Reader, out io.Writer) error {
	r.calls = append(r.calls, command)
	if stdin != nil {
		body, _ := io.ReadAll(stdin)
		if r.stdins == nil {
			r.stdins = map[string]string{}
		}
		r.stdins[command] = string(body)
	}
	for fragment, err := range r.fail {
		if strings.Contains(command, fragment) {
			return err
		}
	}
	for fragment, answer := range r.answers {
		if strings.Contains(command, fragment) && out != nil {
			_, _ = io.WriteString(out, answer)
		}
	}
	return nil
}

func (r *recShell) Upload(local, remote string) error {
	r.uploads = append(r.uploads, local+" -> "+remote)
	return nil
}

func (r *recShell) first(fragment string) string {
	for _, c := range r.calls {
		if strings.Contains(c, fragment) {
			return c
		}
	}
	return ""
}

func testMachine(sh *recShell) (*sshMachine, *bufReporter) {
	report := &bufReporter{}
	m := &sshMachine{
		sh: sh, node: inspector.Node{Host: ip1, User: "root"}, wallet: testEVM, report: report, close: func() {},
		ensureArchive: func(inspector.Node, string, []string) error { return nil },
		waitReady:     func(inspector.Node, time.Duration) error { return nil },
		startTunnel: func(_ context.Context, _ inspector.Node, remote string) (string, func(), error) {
			return "127.0.0.1:40000", func() {}, nil
		},
	}
	return m, report
}

func TestSSHMachine_probeParsesTheFacts(t *testing.T) {
	sh := &recShell{answers: map[string]string{"nproc": factsOutput}}
	m, _ := testMachine(sh)
	f, err := m.Probe(context.Background())
	if err != nil || f.Arch != "amd64" || !f.ClusterInstalled {
		t.Fatalf("%+v, %v", f, err)
	}
	if !strings.HasPrefix(sh.calls[0], "bash -c ") {
		t.Errorf("the probe runs under bash: %s", sh.calls[0])
	}
}

func TestSSHMachine_probeFailureIsReturned(t *testing.T) {
	sh := &recShell{fail: map[string]error{"nproc": errors.New("connection reset")}}
	m, _ := testMachine(sh)
	if _, err := m.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("got %v", err)
	}
}

func TestSSHMachine_stageReleaseVerifiesAgainstTheOperatorsWallet(t *testing.T) {
	m, _ := testMachine(&recShell{})
	var gotArchive string
	var gotTrusted []string
	m.ensureArchive = func(_ inspector.Node, archive string, trusted []string) error {
		gotArchive, gotTrusted = archive, trusted
		return nil
	}
	if err := m.StageRelease(context.Background(), &Release{ArchivePath: "/tmp/endorsed.tar.gz"}); err != nil {
		t.Fatal(err)
	}
	if gotArchive != "/tmp/endorsed.tar.gz" || len(gotTrusted) != 1 || gotTrusted[0] != testEVM {
		t.Errorf("archive %q trusted %v", gotArchive, gotTrusted)
	}
}

func TestSSHMachine_genesisClusterInstallCarriesNoInvite(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	err := m.InstallCluster(context.Background(), ClusterInstall{Create: true, IP: ip1, User: "root", Env: "stagenet-alice", Wallet: testEVM})
	if err != nil {
		t.Fatal(err)
	}
	cmd := sh.calls[0]
	for _, want := range []string{"node install", "--vps-ip '" + ip1 + "'", "--operator-wallet '" + testEVM + "'", "--environment 'stagenet-alice'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--secrets-stdin") || strings.Contains(cmd, "--expect-archive-signers") || len(sh.stdins) != 0 {
		t.Errorf("a genesis node joins nothing: %s", cmd)
	}
}

func TestSSHMachine_joinInvitesTravelOnStdinNotTheCommandLine(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	invite := "orama1_" + strings.Repeat("a", 20)
	err := m.InstallCluster(context.Background(), ClusterInstall{IP: ip2, User: "root", Env: "e", Wallet: testEVM, Invite: invite, Signers: []string{testEVM}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := sh.calls[0]
	if strings.Contains(cmd, invite) {
		t.Errorf("the invite is on the command line, where ps shows it: %s", cmd)
	}
	if !strings.Contains(cmd, "--secrets-stdin") || !strings.Contains(cmd, "--expect-archive-signers '"+testEVM+"'") {
		t.Errorf("command: %s", cmd)
	}
	if !strings.Contains(sh.stdins[cmd], invite) {
		t.Errorf("stdin %q lacks the invite", sh.stdins[cmd])
	}
}

func TestSSHMachine_domainClusterNodesAreNameservers(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	if err := m.InstallCluster(context.Background(), ClusterInstall{Create: true, IP: ip1, User: "root", Env: "e", Wallet: testEVM, Domain: "cluster.example.org"}); err != nil {
		t.Fatal(err)
	}
	if cmd := sh.calls[0]; !strings.Contains(cmd, "--nameserver") || !strings.Contains(cmd, "--base-domain 'cluster.example.org'") {
		t.Errorf("command: %s", cmd)
	}
}

func realInvite(t *testing.T) string {
	t.Helper()
	inv, err := invite.Encode(invite.Invite{JoinURL: "https://" + ip1, Token: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestSSHMachine_mintInviteReadsTheAnchorToo(t *testing.T) {
	want := realInvite(t)
	sh := &recShell{answers: map[string]string{"node invite": want + "\n", "cat /etc/orama/archive-signers": testEVM + "\n"}}
	m, _ := testMachine(sh)
	got, signers, err := m.MintInvite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want || len(signers) != 1 || !strings.EqualFold(signers[0], testEVM) {
		t.Errorf("%q %v", got, signers)
	}
}

func TestSSHMachine_mintInviteRefusesAnInviteThatIsNotOne(t *testing.T) {
	sh := &recShell{answers: map[string]string{"node invite": "; rm -rf /\n"}}
	m, _ := testMachine(sh)
	if _, _, err := m.MintInvite(context.Background()); err == nil {
		t.Fatal("what the node printed goes into a root command; anything but an invite is refused")
	}
}

func globalIn() GlobalInstall {
	in := joinInstall("root", install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider)
	in.Genesis = []byte(`{"chain_id":"x"}`)
	return in
}

func TestSSHMachine_installGlobalOrderAndCleanup(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	if err := m.InstallGlobal(context.Background(), globalIn()); err != nil {
		t.Fatal(err)
	}
	wants := []string{"install -d -m 0700", "cat > /opt/orama/.setup-global/genesis.json", "global install --colocated", "global start chain ipfs", "sudo rm -rf /opt/orama/.setup-global"}
	last := -1
	for _, want := range wants {
		found := -1
		for i, c := range sh.calls {
			if strings.Contains(c, want) {
				found = i
				break
			}
		}
		if found <= last {
			t.Fatalf("%q ran at %d, after %d; calls:\n%s", want, found, last, strings.Join(sh.calls, "\n"))
		}
		last = found
	}
	if got := sh.stdins[sh.first("genesis.json")]; got != `{"chain_id":"x"}` {
		t.Errorf("the genesis is sent on stdin, got %q", got)
	}
}

func TestSSHMachine_installGlobalRemovesTheGenesisEvenWhenTheInstallFails(t *testing.T) {
	sh := &recShell{fail: map[string]error{"global install": errors.New("exit status 1")}}
	m, _ := testMachine(sh)
	err := m.InstallGlobal(context.Background(), globalIn())
	if err == nil || !strings.Contains(err.Error(), "orama global install") {
		t.Fatalf("got %v", err)
	}
	if sh.first("sudo rm -rf /opt/orama/.setup-global") == "" {
		t.Error("the genesis directory must go whatever happens")
	}
	if sh.first("global start") != "" {
		t.Error("nothing is started after a failed install")
	}
}

func TestSSHMachine_installGlobalPutsTheTorNetworkBesideTheBinaries(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	in := joinInstall("root", install.GlobalServiceChain, install.GlobalServiceRelay)
	in.Genesis, in.TorNetwork = []byte("{}"), []byte(`{"name":"tor"}`)
	if err := m.InstallGlobal(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	cmd := sh.first("tor-network.json")
	if cmd == "" || sh.stdins[cmd] != `{"name":"tor"}` {
		t.Errorf("the Tor network file is not written from stdin: %v", sh.calls)
	}
}

func TestSSHMachine_chainStateAndIdentity(t *testing.T) {
	sh := &recShell{answers: map[string]string{
		"__STATUS__": "__STATUS__\n" + rpcStatus + "\n__ACTIVE__\nactive\n__LOG__\n",
	}}
	m, _ := testMachine(sh)
	st, err := m.ChainState(context.Background())
	if err != nil || !st.Running || st.Height != 5123 {
		t.Fatalf("%+v, %v", st, err)
	}
	m2, _ := testMachine(&recShell{answers: map[string]string{"__NODE_ID__": identityOutput(t, strings.Repeat("ab", 20), make([]byte, 32), nil)}})
	id, err := m2.Identity(context.Background(), IdentityRequest{ChainID: testChainID, Operator: testOperator})
	if err != nil || id.ChainNodeID != strings.Repeat("ab", 20) {
		t.Fatalf("%+v, %v", id, err)
	}
}

func TestSSHMachine_startServicesSendsTheNodeIDOnStdin(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	if err := m.StartServices(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if sh.stdins[sh.calls[0]] != "alice" {
		t.Errorf("stdin %q", sh.stdins[sh.calls[0]])
	}
}

func TestSSHMachine_restartForceOnlyWhenAsked(t *testing.T) {
	for _, force := range []bool{false, true} {
		sh := &recShell{}
		m, _ := testMachine(sh)
		waited := false
		m.waitReady = func(inspector.Node, time.Duration) error { waited = true; return nil }
		if err := m.RestartNode(context.Background(), time.Minute, force); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(sh.calls[0], "--force"); got != force || !strings.Contains(sh.calls[0], "node restart") {
			t.Errorf("force=%v: %s", force, sh.calls[0])
		}
		if !waited {
			t.Error("a restart is followed by the health gate")
		}
	}
}

func TestSSHMachine_restartThatNeverComesBackIsAnError(t *testing.T) {
	m, _ := testMachine(&recShell{})
	m.waitReady = func(inspector.Node, time.Duration) error { return errors.New("did not come back within 1m") }
	if err := m.RestartNode(context.Background(), time.Minute, false); err == nil || !strings.Contains(err.Error(), "did not come back") {
		t.Fatalf("got %v", err)
	}
}

func TestSSHMachine_openChainReturnsAnHTTPURL(t *testing.T) {
	m, _ := testMachine(&recShell{})
	var asked string
	m.startTunnel = func(_ context.Context, _ inspector.Node, remote string) (string, func(), error) {
		asked = remote
		return "127.0.0.1:41000", func() {}, nil
	}
	url, stop, err := m.OpenChain(context.Background())
	if err != nil || url != "http://127.0.0.1:41000" || asked != "198.18.0.2:31003" {
		t.Fatalf("%q %q %v", url, asked, err)
	}
	stop()
}

func TestLineWriter_splitsLinesAndSkipsBlanks(t *testing.T) {
	r := &bufReporter{}
	w := &lineWriter{prefix: ip1, report: r}
	fmt.Fprint(w, "first\n\nsec")
	fmt.Fprint(w, "ond\r\nthird")
	got := r.text()
	if got != "  ["+ip1+"] first\n  ["+ip1+"] second" {
		t.Errorf("got %q", got)
	}
}

func TestTail(t *testing.T) {
	if tail("  short \n", 100) != "short" {
		t.Error("trims")
	}
	if got := tail(strings.Repeat("x", 50), 10); got != "..."+strings.Repeat("x", 10) {
		t.Errorf("got %q", got)
	}
}

func TestLineWriter_aMachineCannotDriveTheTerminal(t *testing.T) {
	r := &bufReporter{}
	w := &lineWriter{prefix: ip1, report: r}
	fmt.Fprint(w, "ok\x1b[2J\x1b]0;owned\x07 done\tx\n")
	got := r.text()
	if strings.ContainsAny(got, "\x1b\x07") || !strings.Contains(got, "ok?[2J?]0;owned? done\tx") {
		t.Errorf("got %q: control characters must be replaced, tabs kept", got)
	}
}

func TestLineWriter_flushReportsTheUnfinishedLastLine(t *testing.T) {
	r := &bufReporter{}
	w := &lineWriter{prefix: ip1, report: r}
	fmt.Fprint(w, "one\nlast without a newline")
	w.Flush()
	if got := r.text(); got != "  ["+ip1+"] one\n  ["+ip1+"] last without a newline" {
		t.Errorf("got %q", got)
	}
	w.Flush()
	if strings.Count(r.text(), "last") != 1 {
		t.Error("a flushed line is not reported twice")
	}
}

func TestLineWriter_aLineWithoutAnEndIsCutNotKeptForever(t *testing.T) {
	r := &bufReporter{}
	w := &lineWriter{prefix: ip1, report: r}
	chunk := strings.Repeat("x", 1024)
	for range maxLineBytes/1024 + 2 {
		fmt.Fprint(w, chunk)
	}
	if len(w.buf) > maxLineBytes {
		t.Errorf("%d bytes are being kept for a line that never ends", len(w.buf))
	}
	if r.text() == "" {
		t.Error("the cut line is reported")
	}
}

func TestCapped_refusesWhatPassesTheLimit(t *testing.T) {
	var c capped
	if _, err := c.Write(make([]byte, maxCaptureBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("one more")); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("got %v: a machine that prints without end must fail the command", err)
	}
}

func TestTail_isCleaned(t *testing.T) {
	if got := tail("bad\x1b[31m", 100); strings.ContainsRune(got, '\x1b') {
		t.Errorf("got %q", got)
	}
}

func TestTailBuffer_keepsOnlyTheEnd(t *testing.T) {
	b := &tailBuffer{max: 10}
	for range 1000 {
		fmt.Fprint(b, "0123456789")
	}
	fmt.Fprint(b, "END")
	if got := b.String(); len(got) != 10 || !strings.HasSuffix(got, "END") {
		t.Errorf("got %q: only the last bytes of a machine's stderr are kept", got)
	}
}
