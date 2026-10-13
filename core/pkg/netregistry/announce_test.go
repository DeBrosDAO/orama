package netregistry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func announceInput(dir string) AnnounceInput {
	return AnnounceInput{
		Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", ReleaseRoot: testRoot, Channel: ChannelNightly,
		MinVersion: "0.3.0", ReleaseRepo: "https://releases.example.org/", Seeds: []string{"seed1.teststage.example.org"}, Faucet: true,
	}
}

func TestParseManifest_anAnnouncedNetworkIsValid(t *testing.T) {
	m, err := ParseManifest(marshalManifest(t, announcedManifest()))
	if err != nil {
		t.Fatalf("an announcement was refused: %v", err)
	}
	if !m.Announced() || m.ChainID != "orama-teststage-1" {
		t.Errorf("parsed %+v", m)
	}
}

func TestParseManifest_anAnnouncementMayCarryNoSeeds(t *testing.T) {
	a := announcedManifest()
	a.Seeds = nil
	if _, err := ParseManifest(marshalManifest(t, a)); err != nil {
		t.Fatalf("an announcement without seeds was refused: %v", err)
	}
	created := validManifest()
	created.Seeds = nil
	if _, err := ParseManifest(marshalManifest(t, created)); err == nil {
		t.Error("a created network without seeds was accepted")
	}
}

func TestManifestValidate_anAnnouncementStillNeedsEveryOtherField(t *testing.T) {
	for name, edit := range map[string]func(*Manifest){
		"no chain id":          func(m *Manifest) { m.ChainID = "" },
		"no release repo":      func(m *Manifest) { m.ReleaseRepo = "" },
		"no release root":      func(m *Manifest) { m.ReleaseRootSHA256 = "" },
		"no channel":           func(m *Manifest) { m.Channel = "" },
		"no min version":       func(m *Manifest) { m.MinVersion = "" },
		"a seed that is an IP": func(m *Manifest) { m.Seeds = []string{"203.0.113.9"} },
		"a malformed digest":   func(m *Manifest) { m.GenesisSHA256 = "abcd" },
	} {
		t.Run(name, func(t *testing.T) {
			m := announcedManifest()
			edit(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("Validate accepted it")
			}
		})
	}
}

func TestManifestVerifyGenesis_anAnnouncedNetworkHasNoneToCheck(t *testing.T) {
	m := announcedManifest()
	err := m.VerifyGenesis(testGenesis)
	if !errors.Is(err, ErrNotCreated) || !strings.Contains(err.Error(), "teststage has not been created yet; its creator runs orama setup --create-network teststage") {
		t.Fatalf("VerifyGenesis = %v", err)
	}
	if err := m.CheckCreated(); !errors.Is(err, ErrNotCreated) {
		t.Errorf("CheckCreated = %v", err)
	}
	created := validManifest()
	if err := created.CheckCreated(); err != nil {
		t.Errorf("a created network is joinable: %v", err)
	}
}

func TestManifestMarshal_anAnnouncementWithoutSeedsListsNone(t *testing.T) {
	m := announcedManifest()
	m.Seeds = nil
	out, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"seeds": []`) || !strings.Contains(string(out), `"genesis_sha256": ""`) {
		t.Errorf("marshaled:\n%s", out)
	}
}

func TestFetchGenesis_anAnnouncedNetworkIsNotFetched(t *testing.T) {
	files := map[string][]byte{ManifestFile: marshalManifest(t, announcedManifest()), ReleaseRootFile: testRoot}
	srv, client := networkServer(t, files)
	n, err := FetchNetwork(context.Background(), client, srv.URL+"/nets/teststage/manifest.json")
	if err != nil {
		t.Fatalf("an announced network can be added by URL: %v", err)
	}
	if _, err := n.FetchGenesis(context.Background(), client); !errors.Is(err, ErrNotCreated) {
		t.Fatalf("FetchGenesis = %v, want ErrNotCreated", err)
	}
}

func TestLoadRegistry_loadsAnAnnouncedNetworkWithoutAGenesis(t *testing.T) {
	r, err := LoadFS(networkFS(t, "teststage", announcedManifest(), testRoot), "embedded")
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Get("teststage")
	if err != nil || !n.Manifest.Announced() {
		t.Fatalf("Get = %+v, %v", n, err)
	}
	broken := fstest.MapFS{"embedded/teststage/" + ManifestFile: {Data: marshalManifest(t, announcedManifest())}, "embedded/teststage/" + ReleaseRootFile: {Data: []byte(`{"tampered":true}`)}}
	if _, err := LoadFS(broken, "embedded"); err == nil {
		t.Error("an announcement whose release root is not the pinned one was loaded")
	}
}

func TestAnnounce_writesAnAnnouncementThatLoads(t *testing.T) {
	dir := t.TempDir()
	m, err := Announce(announceInput(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !m.Announced() || m.ReleaseRootSHA256 != Digest(testRoot) || !m.Faucet {
		t.Errorf("manifest = %+v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "teststage", GenesisFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an announcement carries no genesis.json: %v", err)
	}
	n, err := loadNetworkDir(os.DirFS(filepath.Join(dir, "teststage")), ".", "teststage")
	if err != nil || !n.Manifest.Announced() {
		t.Fatalf("what Announce wrote does not load: %v", err)
	}
}

func TestAnnounce_announcingAgainReplacesTheAnnouncement(t *testing.T) {
	dir := t.TempDir()
	if _, err := Announce(announceInput(dir)); err != nil {
		t.Fatal(err)
	}
	in := announceInput(dir)
	in.ChainID, in.Faucet = "orama-teststage-2", false
	m, err := Announce(in)
	if err != nil || m.ChainID != "orama-teststage-2" || m.Faucet {
		t.Fatalf("Announce = %+v, %v", m, err)
	}
}

func TestAnnounce_refusesANetworkThatIsAlreadyCreated(t *testing.T) {
	dir := t.TempDir()
	firstPublish(t, dir)
	_, err := Announce(announceInput(dir))
	if !errors.Is(err, ErrAlreadyCreated) || !strings.Contains(err.Error(), "--chain-id <new chain id>") {
		t.Fatalf("Announce = %v, want ErrAlreadyCreated and the way to reset", err)
	}
	if onDisk, _ := os.ReadFile(filepath.Join(dir, "teststage", GenesisFile)); string(onDisk) != string(testGenesis) {
		t.Error("a refused announcement changed the published network")
	}
}

func TestAnnounce_inputErrors(t *testing.T) {
	for name, edit := range map[string]func(*AnnounceInput){
		"a release root that is not JSON": func(in *AnnounceInput) { in.ReleaseRoot = []byte("x") },
		"a bad channel":                   func(in *AnnounceInput) { in.Channel = "beta" },
		"a release repo over http":        func(in *AnnounceInput) { in.ReleaseRepo = "http://releases.example.org" },
		"a bad chain id":                  func(in *AnnounceInput) { in.ChainID = "Orama" },
	} {
		t.Run(name, func(t *testing.T) {
			in := announceInput(t.TempDir())
			edit(&in)
			if _, err := Announce(in); err == nil {
				t.Fatal("Announce accepted it")
			}
			if entries, _ := os.ReadDir(in.Dir); len(entries) != 0 {
				t.Errorf("a refused announcement left %d entries behind", len(entries))
			}
		})
	}
}

func TestPublish_writesTheFullManifestOverAnAnnouncement(t *testing.T) {
	dir := t.TempDir()
	in := announceInput(dir)
	in.Seeds = []string{"seed7.teststage.example.org"}
	if _, err := Announce(in); err != nil {
		t.Fatal(err)
	}
	m, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis})
	if err != nil {
		t.Fatalf("publishing over an announcement: %v", err)
	}
	if m.Announced() || m.GenesisSHA256 != Digest(testGenesis) || m.Seeds[0] != "seed7.teststage.example.org" || !m.Faucet || m.ReleaseRootSHA256 != Digest(testRoot) {
		t.Errorf("manifest = %+v: the announcement's facts are kept and the genesis is pinned", m)
	}
	n, err := loadNetworkDir(os.DirFS(filepath.Join(dir, "teststage")), ".", "teststage")
	if err != nil || n.Manifest.Announced() {
		t.Fatalf("the published network is still announced: %v", err)
	}
}

func TestPublish_overAnAnnouncementWithAnotherChainID(t *testing.T) {
	dir := t.TempDir()
	if _, err := Announce(announceInput(dir)); err != nil {
		t.Fatal(err)
	}
	next := []byte(`{"chain_id":"orama-teststage-2"}`)
	m, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-2", Genesis: next})
	if err != nil || m.ChainID != "orama-teststage-2" || m.Announced() {
		t.Fatalf("Publish = %+v, %v: an announcement has no genesis to protect", m, err)
	}
}

func TestPublish_aPublishedChainIDStillKeepsItsGenesisAfterAnAnnouncement(t *testing.T) {
	dir := t.TempDir()
	if _, err := Announce(announceInput(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis}); err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"chain_id":"orama-teststage-1","app_state":{"changed":true}}`)
	if _, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: changed}); !errors.Is(err, ErrGenesisChange) {
		t.Fatalf("Publish = %v, want ErrGenesisChange", err)
	}
}
