package setup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// torCreateOptions is a creation on the Tor test machines with the Tor flag as given.
func torCreateOptions(n int, set bool, ips ...string) Options {
	o := createOptions(ips...)
	o.Create.TorAuthorities, o.Create.TorAuthoritiesSet = n, set
	return o
}

func TestPrepareTor_theDefaultIsThreeAuthoritiesWhenThereAreThreeMachines(t *testing.T) {
	cases := []struct {
		name  string
		opts  func() Options
		count int
	}{
		{"three machines", func() Options { return torCreateOptions(0, false, torIPs[:3]...) }, 3},
		{"five machines", func() Options { return torCreateOptions(0, false, torIPs...) }, 3},
		{"two machines", func() Options { return torCreateOptions(0, false, torIPs[:2]...) }, 0},
		{"one machine", func() Options { return torCreateOptions(0, false, torIPs[:1]...) }, 0},
		{"the relays are given another Tor network", func() Options {
			o := torCreateOptions(0, false, torIPs...)
			o.TorNetwork = "tor-network.json"
			return o
		}, 0},
		{"no relay was asked for", func() Options {
			o := torCreateOptions(0, false, torIPs...)
			o.NoRelay = true
			return o
		}, 0},
		{"opted out", func() Options { return torCreateOptions(0, true, torIPs...) }, 0},
		{"four authorities asked for", func() Options { return torCreateOptions(4, true, torIPs...) }, 4},
		{"all machines are authorities", func() Options { return torCreateOptions(5, true, torIPs...) }, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := c.opts()
			if err := o.Normalize(); err != nil {
				t.Fatal(err)
			}
			if o.Create.TorAuthorities != c.count {
				t.Errorf("authorities = %d, want %d", o.Create.TorAuthorities, c.count)
			}
			// Normalizing again, as the wizard and the run both do, gives the same answer.
			if err := o.Normalize(); err != nil || o.Create.TorAuthorities != c.count {
				t.Errorf("normalized twice: %d, %v", o.Create.TorAuthorities, err)
			}
		})
	}
}

func TestPrepareTor_refusalsSayWhy(t *testing.T) {
	cases := []struct {
		name string
		opts func() Options
		want string
	}{
		{"a negative number", func() Options { return torCreateOptions(-1, true, torIPs...) }, "give 0 to run no Tor network"},
		{"one authority", func() Options { return torCreateOptions(1, true, torIPs...) }, "fewer cannot lose one and still hold a majority"},
		{"two authorities", func() Options { return torCreateOptions(2, true, torIPs...) }, "at least 3 directory authorities"},
		{"more authorities than machines", func() Options { return torCreateOptions(4, true, torIPs[:3]...) }, "needs 4 machines, one for each authority, and this run has 3"},
		{"another Tor network at the same time", func() Options {
			o := torCreateOptions(3, true, torIPs...)
			o.TorNetwork = "tor-network.json"
			return o
		}, "pass one of them"},
		{"no relay at the same time", func() Options {
			o := torCreateOptions(3, true, torIPs...)
			o.NoRelay = true
			return o
		}, "--tor-authorities 0"},
		{"an exit and no machine left for a relay", func() Options {
			o := torCreateOptions(3, true, torIPs[:3]...)
			o.Exit = true
			return o
		}, "add machines for the relays"},
		{"an exit asked for and nothing else: the default makes every machine an authority", func() Options {
			o := torCreateOptions(0, false, torIPs[:3]...)
			o.Exit = true
			return o
		}, "with 3 machines all are directory authorities by default, and an authority is never an exit: --exit needs a machine that is not one"},
		{"machines in a reserved range", func() Options { return torCreateOptions(3, true, nIPs(3)...) }, "cannot be in a Tor network"},
		{"the default on machines in a reserved range", func() Options { return torCreateOptions(0, false, nIPs(3)...) }, "--tor-authorities 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := c.opts()
			err := o.Normalize()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if clierr.CodeOf(err) != clierr.CodeUsage {
				t.Errorf("a refusal before any machine is touched is a usage error: %v", err)
			}
		})
	}
}

func TestPrepareTor_aNetworkNameThatCannotNameATorNetworkIsRefused(t *testing.T) {
	o := torCreateOptions(3, true, torIPs...)
	o.Create.Name = "x"
	if err := o.prepareTor(); err == nil || !strings.Contains(err.Error(), "cannot name a Tor network") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateOptions_commandLineRepeatsTheTorFlagOnlyWhenItWasGiven(t *testing.T) {
	given := torCreateOptions(4, true, torIPs...)
	if err := given.Normalize(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(given.Create.commandLine(), " "); !strings.Contains(got, "--tor-authorities 4") {
		t.Errorf("command line = %s", got)
	}
	optedOut := torCreateOptions(0, true, torIPs...)
	if err := optedOut.Normalize(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(optedOut.Create.commandLine(), " "); !strings.Contains(got, "--tor-authorities 0") {
		t.Errorf("an opt-out must be repeated, or the next run makes a Tor network: %s", got)
	}
	defaulted := torCreateOptions(0, false, torIPs...)
	if err := defaulted.Normalize(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(defaulted.Create.commandLine(), " "); strings.Contains(got, "tor-authorities") {
		t.Errorf("the default is not repeated: %s", got)
	}
}

func torPlan(t *testing.T, o Options) *Plan {
	t.Helper()
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := o.Create.manifest(strings.Repeat("a", 64), len(o.IPs), "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildCreatePlan(o, m, "stagenet")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildCreatePlan_theFirstMachinesAreAuthoritiesTheOthersRelays(t *testing.T) {
	o := torCreateOptions(3, true, torIPs...)
	o.Exit = true
	o.Yes = true
	p := torPlan(t, o)
	for i, n := range p.Nodes {
		wantRole, wantExit := TorRelay, true
		if i < 3 {
			wantRole, wantExit = TorDirauth, false
		}
		if n.Tor != wantRole || n.Exit != wantExit {
			t.Errorf("node %d: Tor %q exit %v, want %q %v", i, n.Tor, n.Exit, wantRole, wantExit)
		}
		if n.HasService(install.GlobalServiceRelay) || n.HasService(install.GlobalServiceDirauth) || slices.Contains(n.ServiceNames(), "exit") {
			t.Errorf("node %d installs a Tor role with the chain: %v", i, n.ServiceNames())
		}
		wantRoles := []int{clusterreg.RoleStorage}
		if wantRole == TorRelay {
			wantRoles = append(wantRoles, clusterreg.RoleRelay, clusterreg.RoleExit)
		}
		if !slices.Equal(n.Roles, wantRoles) {
			t.Errorf("node %d registers roles %v, want %v", i, n.Roles, wantRoles)
		}
	}
	summary := strings.Join(p.Summary(), "\n")
	for _, want := range []string{"Tor directory authority", "EXIT relay", "3 machines run directory authorities", "the other 2 run EXIT relays", "two hours or more", "30m0s apart",
		"with its passphrase beside it on the same machine", "a test network's arrangement"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the plan the operator confirms lacks %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "no relay:") {
		t.Errorf("the plan says no relay is run:\n%s", summary)
	}
}

func TestBuildCreatePlan_withoutATorNetworkTheNodesHaveNoTorRole(t *testing.T) {
	p := torPlan(t, torCreateOptions(0, true, torIPs...))
	for i, n := range p.Nodes {
		if n.Tor != "" || n.HasService(install.GlobalServiceRelay) {
			t.Errorf("node %d has a Tor role: %+v", i, n)
		}
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "no relay:") {
		t.Errorf("the plan does not say why no relay runs: %v", p.Notes)
	}
}

func TestTorReady(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ready := func() TorState {
		return TorState{Installed: true, Active: true, Now: now, Info: &tornet.NodeInfo{
			Fingerprint: strings.Repeat("A", 40),
			Consensus:   &tornet.ConsensusInfo{Valid: true, Signatures: 2, Listed: true, ListedFlags: []string{"Fast", "Running", "Valid"}, ValidAfter: now.Add(-10 * time.Minute)},
		}}
	}
	cases := []struct {
		name   string
		mutate func(*TorState)
		after  time.Time
		want   string // "" means ready
	}{
		{"a consensus signed by a majority that lists it", func(*TorState) {}, time.Time{}, ""},
		{"not installed", func(s *TorState) { s.Installed = false }, time.Time{}, "not installed"},
		{"not active", func(s *TorState) { s.Active = false }, time.Time{}, "not active"},
		{"no data directory", func(s *TorState) { s.Info = nil }, time.Time{}, "data directory"},
		{"no identity yet", func(s *TorState) { s.Info.Fingerprint = "" }, time.Time{}, "identity"},
		{"no consensus", func(s *TorState) { s.Info.Consensus = nil }, time.Time{}, "no consensus yet"},
		{"an expired consensus", func(s *TorState) { s.Info.Consensus.Valid = false }, time.Time{}, "no longer valid"},
		{"one signature of three authorities", func(s *TorState) { s.Info.Consensus.Signatures = 1 }, time.Time{}, "a majority (2) is needed"},
		{"not listed", func(s *TorState) { s.Info.Consensus.Listed = false }, time.Time{}, "does not list it"},
		{"listed without the Running flag", func(s *TorState) { s.Info.Consensus.ListedFlags = []string{"Fast", "Valid"} }, time.Time{}, "without the Running flag"},
		{"listed with no flags", func(s *TorState) { s.Info.Consensus.ListedFlags = nil }, time.Time{}, "without the Running flag"},
		{"the consensus from before a restart", func(*TorState) {}, now.Add(-5 * time.Minute), "from before its restart"},
		{"a consensus made after the restart", func(*TorState) {}, now.Add(-15 * time.Minute), ""},
		{"a consensus made at the moment of the restart", func(*TorState) {}, now.Add(-10 * time.Minute), "from before its restart"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := ready()
			c.mutate(&st)
			ok, why := torReady(st, 3, c.after)
			if ok != (c.want == "") || !strings.Contains(why, c.want) {
				t.Errorf("ready = %v (%q), want reason %q", ok, why, c.want)
			}
		})
	}
	// Two authorities of four are not a majority; three are.
	st := ready()
	if ok, _ := torReady(st, 4, time.Time{}); ok {
		t.Error("2 signatures of 4 authorities counted as a majority")
	}
	st.Info.Consensus.Signatures = 3
	if ok, why := torReady(st, 4, time.Time{}); !ok {
		t.Errorf("3 signatures of 4 authorities is a majority: %s", why)
	}
}

// refusingHost is a Tor host whose restarts answer from a script.
type refusingHost struct {
	TorHost
	answers []error
	calls   int
}

func (h *refusingHost) RestartTor(context.Context, TorRole) error {
	i := min(h.calls, len(h.answers)-1)
	h.calls++
	return h.answers[i]
}

func torRestartRunner() *runner {
	rep := &bufReporter{}
	return &runner{d: Deps{Report: rep, Timing: Timing{TorPoll: time.Millisecond, TorRollDeadline: 200 * time.Millisecond}}}
}

func refusal() error {
	return notYetError("1.2.3.4", fmt.Sprintf("%s (an authority casts no Running vote for 30m0s): 57.129.166.16 published its descriptor 4m0s ago", tornet.AuthorityRollRefusal))
}

// The code the run matches is the one a real failed command carries: an *exec.ExitError
// that the SSH session wraps as sshShell.Run does, with the command's own exit status.
func TestRestartNotYet_isTheExitCodeOfTheRefusalAndNothingElse(t *testing.T) {
	run := func(code int) error {
		err := exec.Command("sh", "-c", fmt.Sprintf("echo %q >&2; exit %d", tornet.AuthorityRollRefusal, code)).Run()
		return fmt.Errorf("restart the dirauth: run on 1.2.3.4: %w: %s", err, tornet.AuthorityRollRefusal)
	}
	if !restartNotYet(run(clierr.CodeNotYet)) {
		t.Error("the exit code of the roll refusal was not recognised")
	}
	if restartNotYet(run(clierr.CodeFailure)) {
		t.Error("a failure whose output quotes the refusal text was taken for it")
	}
	if restartNotYet(errors.New(tornet.AuthorityRollRefusal)) || restartNotYet(nil) {
		t.Error("an error without an exit status was taken for the refusal")
	}
}

func TestTorRestart_aFailureThatQuotesTheRefusalTextIsNotWaitedFor(t *testing.T) {
	r := torRestartRunner()
	host := &refusingHost{answers: []error{fmt.Errorf("restart the dirauth: run on 1.2.3.4: %w: %s", exitCoded{code: clierr.CodeFailure, msg: "unit failed"}, tornet.AuthorityRollRefusal)}}
	x := &torRun{run: &nodeRun{plan: NodePlan{IP: "1.2.3.4"}}, host: host, role: TorDirauth}
	if err := r.torRestart(context.Background(), x); err == nil || host.calls != 1 {
		t.Fatalf("err = %v after %d attempts: only the exit code of the refusal is waited out", err, host.calls)
	}
}

func TestTorRestart_anAuthorityWaitsOutTheMachinesRefusalAndNothingElse(t *testing.T) {
	r := torRestartRunner()
	host := &refusingHost{answers: []error{refusal(), refusal(), refusal(), nil}}
	x := &torRun{run: &nodeRun{plan: NodePlan{IP: "1.2.3.4"}}, host: host, role: TorDirauth}
	if err := r.torRestart(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if host.calls != 4 {
		t.Errorf("%d restart attempts, want 3 refusals and the restart", host.calls)
	}
	if text := r.d.Report.(*bufReporter).text(); !strings.Contains(text, "waiting to restart the authority on 1.2.3.4") || !strings.Contains(text, "published its descriptor 4m0s ago") {
		t.Errorf("the operator is not told what the run waits for:\n%s", text)
	}
}

func TestTorRestart_anyOtherFailureEndsTheRunAtOnce(t *testing.T) {
	r := torRestartRunner()
	host := &refusingHost{answers: []error{errors.New("cannot tell whether the other authorities are voting: this authority holds no valid consensus")}}
	x := &torRun{run: &nodeRun{plan: NodePlan{IP: "1.2.3.4"}}, host: host, role: TorDirauth}
	err := r.torRestart(context.Background(), x)
	if err == nil || !strings.Contains(err.Error(), "holds no valid consensus") || host.calls != 1 {
		t.Fatalf("err = %v after %d attempts: a refusal that is not the spacing rule is not waited out", err, host.calls)
	}
}

func TestTorRestart_aRefusalThatNeverEndsGivesUpAtTheDeadline(t *testing.T) {
	r := torRestartRunner()
	host := &refusingHost{answers: []error{refusal()}}
	x := &torRun{run: &nodeRun{plan: NodePlan{IP: "1.2.3.4"}}, host: host, role: TorDirauth}
	err := r.torRestart(context.Background(), x)
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") {
		t.Fatalf("err = %v", err)
	}
}

func TestTorRestart_aRelayIsNeverWaitedFor(t *testing.T) {
	r := torRestartRunner()
	host := &refusingHost{answers: []error{refusal()}}
	x := &torRun{run: &nodeRun{plan: NodePlan{IP: "1.2.3.4"}}, host: host, role: TorRelay}
	if err := r.torRestart(context.Background(), x); err == nil || host.calls != 1 {
		t.Fatalf("a relay's restart that failed was retried: %v after %d attempts", err, host.calls)
	}
}

func TestTorInstallCommand(t *testing.T) {
	dirauth := TorInstallCommand(TorInstall{Role: TorDirauth, IP: "57.129.166.16", Contact: "operator orama1abc", Bundle: "/var/lib/orama-tor-authority/deploy/OramaAuth01"})
	want := "sudo /opt/orama/bin/orama global install --colocated --services 'dirauth' --staged-dir /opt/orama/bin --manifest /opt/orama/manifest.json " +
		"--tor-address '57.129.166.16' --tor-contact 'operator orama1abc' --tor-authority-keys '/var/lib/orama-tor-authority/deploy/OramaAuth01'"
	if dirauth != want {
		t.Errorf("dirauth install:\n got %s\nwant %s", dirauth, want)
	}
	relay := TorInstallCommand(TorInstall{Role: TorRelay, IP: "161.97.184.202", Contact: "c", NodeID: "founder-4"})
	want = "sudo /opt/orama/bin/orama global install --colocated --services 'relay' --staged-dir /opt/orama/bin --manifest /opt/orama/manifest.json " +
		"--tor-address '161.97.184.202' --tor-contact 'c' --tor-node-id 'founder-4'"
	if relay != want {
		t.Errorf("relay install:\n got %s\nwant %s", relay, want)
	}
	exit := TorInstallCommand(TorInstall{Role: TorRelay, IP: "161.97.184.202", Contact: "c", NodeID: "founder-4", Exit: true})
	if !strings.Contains(exit, "--services 'relay,exit'") {
		t.Errorf("exit install: %s", exit)
	}
	for _, c := range []string{dirauth, relay, exit} {
		for _, bad := range []string{"--init-chain", "--genesis", "chain,", "--persistent-peers"} {
			if strings.Contains(c, bad) {
				t.Errorf("a Tor role install touches the chain (%s): %s", bad, c)
			}
		}
	}
	if got := TorInstallCommand(TorInstall{Role: TorRelay, IP: "1.2.3.4", Contact: "x'; reboot #", NodeID: "n"}); strings.Contains(got, "x'; reboot") {
		t.Errorf("the contact reaches the shell unquoted: %s", got)
	}
}

func TestTorLifecycleAndKeysCommands(t *testing.T) {
	if got := torLifecycleCommand("restart", TorDirauth); got != "sudo /opt/orama/bin/orama global restart dirauth" {
		t.Error(got)
	}
	if got := torLifecycleCommand("start", TorRelay); got != "sudo /opt/orama/bin/orama global start relay" {
		t.Error(got)
	}
	got := authorityKeysCommand(AuthorityKeysInput{Nickname: "OramaAuth01", Address: "57.129.166.16"})
	if got != "sudo /opt/orama/bin/orama maint global tor authority-keys --nickname 'OramaAuth01' --address '57.129.166.16'" {
		t.Error(got)
	}
}

func TestParseTorState(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	info := `[{"home":"/var/lib/orama-global/tor-relay","nickname":"X","fingerprint":"` + strings.Repeat("A", 40) + `","consensus":{"flavor":"ns","valid":true,"signatures":3,"listed":true}},` +
		`{"home":"/var/lib/orama-global/tor-onion","error":"unreadable"}]`
	out := func(net, now, unit, active, pending, info string) string {
		return strings.Join([]string{markTorNetwork, net, markTorNow, now, markTorUnit, unit, markTorActive, active, markTorPending, pending, markTorInfo, info}, "\n")
	}
	st, err := ParseTorState(out(sum, "1790000000", "1", "active", "0", info), TorRelay)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || !st.Active || st.RestartPending || st.NetworkSHA256 != sum || st.Now.Unix() != 1790000000 || st.Info == nil || st.Info.Consensus == nil || st.Info.Consensus.Signatures != 3 {
		t.Errorf("state = %+v", st)
	}
	none, err := ParseTorState(out("", "1790000000", "0", "inactive", "0", ""), TorDirauth)
	if err != nil || none.Installed || none.Active || none.Info != nil || none.NetworkSHA256 != "" {
		t.Errorf("a machine without the role = %+v, %v", none, err)
	}
	if pending, _ := ParseTorState(out(sum, "1790000000", "1", "active", "1", ""), TorDirauth); !pending.RestartPending {
		t.Error("a torrc newer than the process was not reported")
	}
	if other, err := ParseTorState(out(sum, "1790000000", "1", "active", "0", info), TorDirauth); err != nil || other.Info != nil {
		t.Errorf("the relay's info was read for a directory authority: %+v, %v", other.Info, err)
	}
	for name, bad := range map[string]string{
		"a bad digest":               out("zz", "1790000000", "1", "active", "0", ""),
		"a clock that is not a time": out(sum, "soon", "1", "active", "0", ""),
		"a unit flag":                out(sum, "1790000000", "2", "active", "0", ""),
		"a pending flag":             out(sum, "1790000000", "1", "active", "x", ""),
		"info that is not JSON":      out(sum, "1790000000", "1", "active", "0", "Error: not root"),
		"a role that cannot be read": out(sum, "1790000000", "1", "active", "0", `[{"home":"/var/lib/orama-global/tor-relay","error":"permission denied"}]`),
	} {
		if _, err := ParseTorState(bad, TorRelay); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	script := torStateScript(TorDirauth)
	for _, want := range []string{"/var/lib/orama-global/tor-network.json", "/etc/systemd/system/orama-global-tor-dirauth.service", "/var/lib/orama-global/tor-dirauth.torrc", "global tor info --json"} {
		if !strings.Contains(script, want) {
			t.Errorf("the state script lacks %q:\n%s", want, script)
		}
	}
}

func TestParseAuthorityKeys_theDocumentIsTheLastLine(t *testing.T) {
	keys := tornet.AuthorityKeys{Created: true, Authority: fakeAuthority(0, AuthorityKeysInput{Nickname: "OramaAuth01", Address: "57.129.166.16"}),
		Expires: time.Date(2027, 10, 11, 0, 0, 0, 0, time.UTC), Bundle: "/var/lib/orama-tor-authority/deploy/OramaAuth01"}
	doc := `{"created":true,"authority":{"nickname":"OramaAuth01","address":"57.129.166.16","or_port":31020,"dir_port":31021,"v3_ident":"` + keys.Authority.V3Ident +
		`","fingerprint":"` + keys.Authority.Fingerprint + `","ed25519_id":"` + keys.Authority.Ed25519ID + `"},"expires":"2027-10-11T00:00:00Z","bundle":"` + keys.Bundle + `"}`
	got, err := ParseAuthorityKeys("some noise a package install printed\n" + doc + "\n")
	if err != nil || got.Authority != keys.Authority || got.Bundle != keys.Bundle {
		t.Fatalf("%+v, %v", got, err)
	}
	for name, bad := range map[string]string{"nothing": "", "noise only": "installed\n", "a document with a hole": `{"created":true}`} {
		if _, err := ParseAuthorityKeys(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestCheckAuthorityKeys(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	want := AuthorityKeysInput{Nickname: "OramaAuth01", Address: "57.129.166.16"}
	good := func() tornet.AuthorityKeys {
		k := tornet.AuthorityKeys{Authority: fakeAuthority(0, want), Expires: now.Add(365 * 24 * time.Hour), Bundle: "/var/lib/orama-tor-authority/deploy/OramaAuth01"}
		return k
	}
	if err := checkAuthorityKeys(good(), want, now); err != nil {
		t.Fatalf("good keys refused: %v", err)
	}
	cases := map[string]struct {
		mutate func(*tornet.AuthorityKeys)
		want   string
	}{
		"another nickname":       {func(k *tornet.AuthorityKeys) { k.Authority.Nickname = "OramaAuth02" }, "not OramaAuth01"},
		"another address":        {func(k *tornet.AuthorityKeys) { k.Authority.Address = "57.129.166.99" }, "not OramaAuth01 at 57.129.166.16"},
		"another OR port":        {func(k *tornet.AuthorityKeys) { k.Authority.ORPort = 9001 }, "global firewall opens 31020/31021"},
		"another dir port":       {func(k *tornet.AuthorityKeys) { k.Authority.DirPort = 80 }, "global firewall opens 31020/31021"},
		"a bundle elsewhere":     {func(k *tornet.AuthorityKeys) { k.Bundle = "/tmp/evil/OramaAuth01" }, "makes it at"},
		"a bundle by traversal":  {func(k *tornet.AuthorityKeys) { k.Bundle = "/var/lib/orama-tor-authority/deploy/../../../etc" }, "makes it at"},
		"a certificate that ran": {func(k *tornet.AuthorityKeys) { k.Expires = now.Add(-time.Hour) }, "expires on"},
		"a certificate about to": {func(k *tornet.AuthorityKeys) { k.Expires = now.Add(torMinCertValidity - time.Hour) }, "less than"},
	}
	for name, c := range cases {
		k := good()
		c.mutate(&k)
		if err := checkAuthorityKeys(k, want, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestTorPublish_theManifestPinsTheFileThatIsPublished(t *testing.T) {
	h := newCreateHarness(t)
	res := h.mustCreate(t, h.torOpts(3, torIPs[:3]...))
	m, file, _ := publishedTor(t, h)
	if m.TorNetworkSHA256 != netregistry.Digest(file) || res.Created.Manifest.TorNetworkSHA256 != m.TorNetworkSHA256 {
		t.Errorf("manifest pin %s, file %s, result %s", m.TorNetworkSHA256, netregistry.Digest(file), res.Created.Manifest.TorNetworkSHA256)
	}
	steps := strings.Join(res.Created.NextSteps(), "\n")
	if !strings.Contains(steps, "tor-network.json") || !strings.Contains(steps, m.TorNetworkSHA256) {
		t.Errorf("the next steps do not mention the pinned Tor network:\n%s", steps)
	}
}
