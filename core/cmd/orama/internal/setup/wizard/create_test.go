package wizard

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// toNetworks answers the questions up to the list of networks.
func (d *driver) toNetworks() {
	d.t.Helper()
	d.wantStep(stepIPs)
	d.text(ipA + " " + ipB)
	d.enter()
	d.enter() // root
	d.enter() // the RootWallet already has a key
	d.press("1")
	d.press("1")
	d.wantStep(stepNetwork)
}

func (d *driver) chooseCreate(rows int) {
	d.t.Helper()
	for range rows {
		d.key(tea.KeyMsg{Type: tea.KeyDown})
	}
	d.enter()
}

func TestWizard_createANetworkAsksTheThreeQuestionsAndSkipsTheOptions(t *testing.T) {
	f := newFake()
	f.services()
	d := newDriver(t, f, setup.Options{})
	d.toNetworks()
	d.wantView("Create a new network")
	d.chooseCreate(1)
	d.wantStep(stepCreateName)
	d.enter()
	d.wantView("give the network a name")
	d.text("StageNet")
	d.enter()
	d.wantStep(stepCreateChainID)
	d.wantView("-stagenet-")
	d.enter()
	d.wantView("give the chain id")
	d.text("orama-stagenet-7")
	d.enter()
	d.wantStep(stepCreateRoot)
	d.enter()
	d.wantView("release-root.json")
	d.text("/tmp/release-root.json")
	d.enter()
	d.wantStep(stepStorage)
	d.enter()
	d.wantStep(stepName)
	d.wantView("founder")
	d.enter() // the default name for the seats
	d.wantStep(stepInspect)
	d.enter()
	d.wantStep(stepConfirm)
	d.wantView("founder (" + ipA + ")")
	d.wantView("founder-2 (" + ipB + ")")
	c := d.m.opts.Create
	if c == nil || c.Name != "stagenet" || c.ChainID != "orama-stagenet-7" || c.ReleaseRoot != "/tmp/release-root.json" || d.m.opts.Network != "" {
		t.Fatalf("options = %+v, create %+v", d.m.opts, c)
	}
	if d.m.opts.Name != setup.DefaultCreateNodeName {
		t.Errorf("name = %q", d.m.opts.Name)
	}
}

func TestWizard_aCliThatKnowsNoNetworkCanStillCreateOne(t *testing.T) {
	f := newFake()
	f.networks = nil
	d := newDriver(t, f, setup.Options{})
	d.toNetworks()
	d.wantView("Create a new network")
	d.enter()
	d.wantStep(stepCreateName)
}

func TestWizard_leavingTheCreationForAnExistingNetworkClearsIt(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toNetworks()
	d.chooseCreate(1)
	d.text("x")
	d.enter()
	d.esc()
	d.esc()
	d.wantStep(stepNetwork)
	d.enter() // stagenet is the first row and the default
	d.wantStep(stepOptions)
	if d.m.opts.Create != nil || d.m.opts.Network != "stagenet" {
		t.Errorf("a join kept the creation: %+v", d.m.opts)
	}
}

func TestWizard_theFinishedCreationShowsHowToPublish(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	m.step = stepDone
	m.result = &setup.Result{Created: &setup.CreatedNetwork{
		Manifest: &netregistry.Manifest{Name: "stagenet", ChainID: "orama-stagenet-7", Seeds: []string{"seed1.stagenet.orama.network"}},
		Dir:      "networks/stagenet", Machines: []string{ipA},
	}}
	view := m.View()
	for _, want := range []string{"make -C core sync-networks", "orama network add https://orama.network/networks/stagenet/manifest.json"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen lacks %q:\n%s", want, view)
		}
	}
}

func TestWizard_aNetworkThatIsAnnouncedIsNotJoinableAndIsCreatedWithOneQuestion(t *testing.T) {
	f := newFake()
	f.networks = append(f.networks, NetworkChoice{Name: "newnet", ChainID: "orama-newnet-stagenet-1", Announced: true})
	d := newDriver(t, f, setup.Options{})
	d.toNetworks()
	if strings.Contains(d.m.View(), "newnet") {
		t.Errorf("an announced network is offered to join:\n%s", d.m.View())
	}
	d.chooseCreate(1)
	d.wantStep(stepCreateName)
	d.text("newnet")
	d.enter()
	d.wantStep(stepStorage)
	if c := d.m.opts.Create; c == nil || c.Name != "newnet" || c.ChainID != "" || c.ReleaseRoot != "" {
		t.Fatalf("create = %+v: the announcement supplies the chain id and the release root", c)
	}
}

func TestWizard_aNetworkThatIsNotAnnouncedStillAsksForTheChainID(t *testing.T) {
	d := newDriver(t, newFake(), setup.Options{})
	d.toNetworks()
	d.chooseCreate(1)
	d.text("othernet")
	d.enter()
	d.wantStep(stepCreateChainID)
}
