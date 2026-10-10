package wizard

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

const (
	ipA = "203.0.113.11"
	ipB = "203.0.113.12"
)

// fakeServices is what the wizard asks of the outside.
type fakeServices struct {
	walletErr error
	networks  []NetworkChoice
	keys      []HostKey
	keysErr   error
	inspect   func(opts setup.Options) []setup.Inspection
	runErr    error
	result    *setup.Result

	inspected []setup.Options
	ran       []setup.Options
}

func (f *fakeServices) services() Services {
	return Services{
		Wallet:   func(context.Context) error { return f.walletErr },
		Networks: func() ([]NetworkChoice, error) { return f.networks, nil },
		HostKeys: func(context.Context, string) ([]HostKey, error) { return f.keys, f.keysErr },
		Inspect: func(_ context.Context, o setup.Options) ([]setup.Inspection, error) {
			f.inspected = append(f.inspected, o)
			if f.inspect != nil {
				return f.inspect(o), nil
			}
			out := make([]setup.Inspection, len(o.IPs))
			for i, ip := range o.IPs {
				out[i] = setup.Inspection{IP: ip, Facts: setup.Facts{Arch: "amd64", Hardware: install.Hardware{CPUCores: 8, RAMBytes: 16 << 30, FreeDiskBytes: 300 << 30}}}
			}
			return out, nil
		},
		Plan: func(_ context.Context, o setup.Options) (*setup.Plan, error) {
			if err := o.Normalize(); err != nil {
				return nil, err
			}
			return setup.BuildPlan(setup.PlanInput{Options: o, Network: &netregistry.Manifest{Name: "stagenet", ChainID: "orama-stagenet-6", Channel: "nightly"}, Env: "stagenet-alice"})
		},
		Run: func(_ context.Context, o setup.Options, rep setup.Reporter) (*setup.Result, error) {
			f.ran = append(f.ran, o)
			rep.Emit(setup.Event{Node: o.IPs[0], Step: setup.StepEnroll, State: setup.StateDone})
			rep.Linef("hello from the run")
			return f.result, f.runErr
		},
	}
}

func newFake() *fakeServices {
	return &fakeServices{
		networks: []NetworkChoice{{Name: "stagenet", ChainID: "orama-stagenet-6", Default: true}},
		keys:     []HostKey{{Type: "ed25519", Fingerprint: "SHA256:edkey"}, {Type: "rsa", Fingerprint: "SHA256:rsakey"}},
		result:   &setup.Result{Operator: "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"},
	}
}

// driver feeds a model keys and runs the commands it returns.
type driver struct {
	t *testing.T
	m *Model
}

func newDriver(t *testing.T, f *fakeServices, preset setup.Options) *driver {
	t.Helper()
	d := &driver{t: t, m: New(context.Background(), f.services(), preset)}
	d.run(d.m.Init())
	return d
}

// run executes cmd and applies the message it returns, repeatedly, until a
// command blocks on the run's feed (which the test feeds by hand) or ends.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	for i := 0; cmd != nil && i < 20; i++ {
		if d.m.step == stepRun && d.m.running {
			return
		}
		msg := cmd()
		if msg == nil {
			return
		}
		if _, isBatch := msg.(tea.BatchMsg); isBatch {
			return
		}
		cmd = d.apply(msg)
	}
}

func (d *driver) apply(msg tea.Msg) tea.Cmd {
	d.t.Helper()
	_, cmd := d.m.Update(msg)
	return cmd
}

func (d *driver) key(k tea.KeyMsg) {
	d.t.Helper()
	d.run(d.apply(k))
}

func (d *driver) text(s string) {
	d.t.Helper()
	d.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func (d *driver) enter() { d.key(tea.KeyMsg{Type: tea.KeyEnter}) }
func (d *driver) esc()   { d.key(tea.KeyMsg{Type: tea.KeyEsc}) }
func (d *driver) press(s string) {
	d.t.Helper()
	d.text(s)
}

func (d *driver) view() string { return d.m.View() }

func (d *driver) wantStep(s step) {
	d.t.Helper()
	if d.m.step != s {
		d.t.Fatalf("on step %d, want %d; view:\n%s", d.m.step, s, d.view())
	}
}

func (d *driver) wantView(substr string) {
	d.t.Helper()
	if !strings.Contains(d.view(), substr) {
		d.t.Fatalf("the screen lacks %q:\n%s", substr, d.view())
	}
}

// toNetworkStep answers the questions up to the host key.
func (d *driver) toOptions() {
	d.t.Helper()
	d.wantStep(stepIPs)
	d.text(ipA + " " + ipB)
	d.enter()
	d.wantStep(stepUser)
	d.enter() // root
	d.wantStep(stepLogin)
	d.enter() // the RootWallet already has a key
	d.wantStep(stepHostKeys)
	d.press("1")
	d.press("1")
	d.wantStep(stepOptions)
}

func TestWizard_aFullRunFromQuestionsToTheFinishedSetup(t *testing.T) {
	f := newFake()
	d := newDriver(t, f, setup.Options{})
	d.toOptions()
	d.enter() // global layer stays on
	d.wantStep(stepStorage)
	d.wantView("50")
	d.enter()
	d.wantStep(stepName)
	d.text("alice")
	d.enter()
	d.wantStep(stepInspect)
	d.wantView(ipA + ": 8 vCPU")
	d.wantView("[ok]")
	d.enter()
	d.wantStep(stepConfirm)
	d.wantView("alice (" + ipA + "): cluster node creates the cluster")
	d.wantView("alice-2 (" + ipB + ")")
	d.wantView("validator")
	d.press("y")
	d.wantStep(stepRun)

	if len(f.inspected) != 1 || len(f.inspected[0].IPs) != 2 {
		t.Fatalf("inspected %+v", f.inspected)
	}
	// The run's events and output reach the screen through the feed.
	d.apply(<-d.m.feed)
	d.apply(<-d.m.feed)
	d.wantView("[ok] enroll")
	d.wantView("hello from the run")
	d.apply(<-d.m.feed) // done
	d.wantStep(stepDone)
	d.wantView("Done.")
	d.wantView("orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53")

	if len(f.ran) != 1 {
		t.Fatalf("ran %d times", len(f.ran))
	}
	got := f.ran[0]
	if got.Name != "alice" || got.StorageGB != 50 || got.ClusterOnly || got.User != "root" || !got.Yes {
		t.Errorf("the run got %+v", got)
	}
	if got.HostKeys[ipA] != "SHA256:edkey" || got.HostKeys[ipB] != "SHA256:edkey" {
		t.Errorf("host keys %v: the fingerprint the person confirmed is the one pinned", got.HostKeys)
	}
	d.enter()
	if o := d.m.Outcome(); !o.OpenStatus || o.Result == nil || o.Err != nil {
		t.Errorf("outcome %+v: enter after a good run opens status", o)
	}
}

func TestWizard_walletNotReadyStopsWithItsMessage(t *testing.T) {
	f := newFake()
	f.walletErr = errors.New("the RootWallet agent is locked")
	d := newDriver(t, f, setup.Options{})
	d.wantStep(stepWallet)
	d.wantView("locked")
	d.press("x")
	if o := d.m.Outcome(); o.Err == nil || !strings.Contains(o.Err.Error(), "locked") || !o.Quit {
		t.Fatalf("outcome %+v", o)
	}
}

func TestWizard_badAddressesAreRefusedOnTheSpot(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.text("10.0.0.5")
	d.enter()
	d.wantStep(stepIPs)
	d.wantView("public")
	d.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.text("not-an-ip")
	d.enter()
	d.wantStep(stepIPs)
}

func TestWizard_presetFlagsStartTheAnswers(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{IPs: []string{ipA}, User: "ubuntu", Name: "bob"})
	d.wantView(ipA)
	d.enter()
	d.wantView("ubuntu")
}

func TestWizard_clusterOnlySkipsStorageAndNeedsNoName(t *testing.T) {
	f := newFake()
	d := newDriver(t, f, setup.Options{})
	d.toOptions()
	d.key(tea.KeyMsg{Type: tea.KeySpace}) // global layer off
	d.wantView("[ ] Run the global layer")
	d.wantView("2 vCPU, 2 GiB")
	d.enter()
	d.wantStep(stepName)
	d.enter() // no name
	d.wantStep(stepInspect)
	d.enter()
	d.wantStep(stepConfirm)
	d.wantView("cluster node creates the cluster")
	d.press("y")
	d.apply(<-d.m.feed)
	d.apply(<-d.m.feed)
	d.apply(<-d.m.feed)
	if got := f.ran[0]; !got.ClusterOnly || got.StorageGB != 0 || got.Name != "" {
		t.Errorf("run options %+v", got)
	}
}

func TestWizard_theExitRoleNeedsTheWarningAccepted(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toOptions()
	down := tea.KeyMsg{Type: tea.KeyDown}
	d.key(down) // relay row
	d.key(tea.KeyMsg{Type: tea.KeySpace})
	d.key(down) // exit row
	d.key(tea.KeyMsg{Type: tea.KeySpace})
	d.wantView("abuse complaints")
	d.wantView("Accept this? (y/n)")
	d.press("n")
	if d.m.toggles[optExit] || d.m.opts.ExitConfirmed {
		t.Fatal("declining the warning must leave the exit off")
	}
	d.key(tea.KeyMsg{Type: tea.KeySpace})
	d.press("y")
	if !d.m.toggles[optExit] || !d.m.opts.ExitConfirmed {
		t.Fatal("accepting the warning turns the exit on and records the consent")
	}
	d.enter()
	d.wantStep(stepStorage)
	d.enter()
	d.wantStep(stepTor) // a relay needs the Tor network file
	d.enter()
	d.wantView("give the path of the Tor network file")
	d.text("/tmp/tor-network.json")
	d.enter()
	d.wantStep(stepName)
	if !d.m.opts.Exit || d.m.opts.TorNetwork != "/tmp/tor-network.json" {
		t.Errorf("options %+v", d.m.opts)
	}
}

func TestWizard_turningTheRelayOffTurnsTheExitOff(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toOptions()
	d.m.toggles[optRelay], d.m.toggles[optExit] = true, true
	d.m.cursor = optRelay
	d.key(tea.KeyMsg{Type: tea.KeySpace})
	if d.m.toggles[optRelay] || d.m.toggles[optExit] {
		t.Fatal("an exit is a relay: no relay, no exit")
	}
	d.m.cursor = optExit
	d.key(tea.KeyMsg{Type: tea.KeySpace})
	if d.m.exitAsked {
		t.Fatal("the exit cannot be chosen without a relay")
	}
}

func TestWizard_aHostKeyThatDoesNotMatchIsRefused(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.wantStep(stepIPs)
	d.text(ipA)
	d.enter()
	d.enter()
	d.enter()
	d.wantStep(stepHostKeys)
	d.wantView("SHA256:edkey")
	d.wantView("SHA256:rsakey")
	d.press("n")
	d.wantView("not confirmed")
	d.wantStep(stepHostKeys)
	d.esc()
	d.wantStep(stepLogin)
}

func TestWizard_aHostKeyScanThatFailsSaysSo(t *testing.T) {
	f := newFake()
	f.keysErr = errors.New("connection refused")
	d := newDriver(t, f, setup.Options{})
	d.text(ipA)
	d.enter()
	d.enter()
	d.enter()
	d.wantView("could not read the SSH host key of " + ipA)
	d.wantView("connection refused")
}

func TestWizard_aTypedPasswordIsNeverDrawn(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.text(ipA)
	d.enter()
	d.enter()
	d.key(tea.KeyMsg{Type: tea.KeyDown})
	d.key(tea.KeyMsg{Type: tea.KeyDown})
	d.enter() // type the password now
	d.wantStep(stepSecret)
	d.text("hunter2-secret")
	d.wantView("**************")
	if strings.Contains(d.view(), "hunter2") {
		t.Fatal("the password is on the screen")
	}
	d.enter()
	d.wantStep(stepHostKeys)
	if d.m.opts.Password != "hunter2-secret" || !d.m.opts.UsePassword {
		t.Errorf("options %+v", d.m.opts)
	}
}

func TestWizard_aKeyFileLogin(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.text(ipA)
	d.enter()
	d.enter()
	d.key(tea.KeyMsg{Type: tea.KeyUp}) // wraps to the last: a key file
	d.enter()
	d.wantStep(stepSecret)
	d.enter()
	d.wantView("give the path of the private key")
	d.text("/home/me/.ssh/id_ed25519")
	d.enter()
	if d.m.opts.BootstrapKey != "/home/me/.ssh/id_ed25519" || d.m.opts.UsePassword {
		t.Errorf("options %+v", d.m.opts)
	}
}

func TestWizard_aMachineBelowTheFloorBlocksTheRun(t *testing.T) {
	f := newFake()
	f.inspect = func(o setup.Options) []setup.Inspection {
		return []setup.Inspection{
			{IP: ipA, Facts: setup.Facts{Arch: "amd64"}},
			{IP: ipB, Err: errors.New("the machine is below the full profile: 2 vCPU (needs 4)")},
		}
	}
	d := newDriver(t, f, setup.Options{})
	d.toOptions()
	d.enter()
	d.enter()
	d.text("alice")
	d.enter()
	d.wantStep(stepInspect)
	d.wantView("[!!]")
	d.wantView("2 vCPU (needs 4)")
	d.enter()
	d.wantStep(stepInspect) // enter does nothing while a machine is refused
	d.esc()
	d.wantStep(stepName)
}

func TestWizard_escGoesBackAndKeepsTheAnswers(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.text(ipA)
	d.enter()
	d.wantStep(stepUser)
	d.esc()
	d.wantStep(stepIPs)
	d.wantView(ipA)
	d.esc() // nowhere further back
	d.wantStep(stepIPs)
}

func TestWizard_confirmNGoesBack(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toOptions()
	d.enter()
	d.enter()
	d.text("alice")
	d.enter()
	d.enter()
	d.wantStep(stepConfirm)
	d.press("n")
	d.wantStep(stepInspect)
}

func TestWizard_aFailedRunShowsWhyAndDoesNotOfferStatus(t *testing.T) {
	f := newFake()
	f.runErr = errors.New("machine 203.0.113.12: install the cluster node: boom")
	f.result = nil
	d := newDriver(t, f, setup.Options{})
	d.toOptions()
	d.enter()
	d.enter()
	d.text("alice")
	d.enter()
	d.enter()
	d.press("y")
	for range 3 {
		d.apply(<-d.m.feed)
	}
	d.wantStep(stepDone)
	d.wantView("Setup stopped")
	d.wantView("boom")
	d.wantView("resumes where it stopped")
	d.enter()
	if o := d.m.Outcome(); o.OpenStatus || o.Err == nil {
		t.Errorf("outcome %+v", o)
	}
}

func TestWizard_ctrlCBeforeTheRunLeavesNothingStarted(t *testing.T) {
	f := newFake()
	d := newDriver(t, f, setup.Options{})
	d.wantStep(stepIPs)
	d.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	if o := d.m.Outcome(); !o.Quit || o.Err == nil {
		t.Errorf("outcome %+v", o)
	}
	if len(f.ran) != 0 {
		t.Error("nothing runs before the confirmation")
	}
	if d.m.ctx.Err() == nil {
		t.Error("quitting cancels the context")
	}
}

func TestWizard_severalNetworksAreChosenFromAList(t *testing.T) {
	f := newFake()
	f.networks = []NetworkChoice{{Name: "stagenet", ChainID: "orama-stagenet-6"}, {Name: "testnet", ChainID: "orama-testnet-2", Default: true}}
	d := newDriver(t, f, setup.Options{})
	d.text(ipA)
	d.enter()
	d.enter()
	d.enter()
	d.press("1")
	d.wantStep(stepNetwork)
	d.wantView("testnet  (chain orama-testnet-2)")
	d.key(tea.KeyMsg{Type: tea.KeyUp})
	d.enter()
	if d.m.opts.Network != "stagenet" {
		t.Errorf("network %q", d.m.opts.Network)
	}
}

func TestWizard_aNameThatIsNotOneIsRefused(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toOptions()
	d.enter()
	d.enter()
	d.text("Not A Name")
	d.enter()
	d.wantStep(stepName)
	d.wantView("lowercase")
}

func TestWizard_storageMustBeANumber(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toOptions()
	d.enter()
	d.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.text("lots")
	d.enter()
	d.wantStep(stepStorage)
	d.wantView("is not a number of gigabytes")
}

func TestWizard_theRunScreenKeepsOnlyTheEndOfTheOutput(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	for i := range maxLines + 50 {
		m.addLine(strings.Repeat("x", 1) + string(rune('a'+i%26)))
	}
	if len(m.lines) != maxLines {
		t.Fatalf("%d lines kept", len(m.lines))
	}
	if got := m.tail(visibleLines); len(got) != visibleLines {
		t.Fatalf("%d lines shown", len(got))
	}
}

func TestInput_editing(t *testing.T) {
	var in input
	in.Placeholder = "hint"
	if !strings.Contains(in.View(), "hint") {
		t.Error("an empty field shows its hint")
	}
	in.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})
	in.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	in.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")})
	if in.Value() != "aé" {
		t.Errorf("value %q", in.Value())
	}
	in.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	in.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if in.Value() != "" {
		t.Errorf("value %q", in.Value())
	}
}
