package netregistry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func firstPublish(t *testing.T, dir string) *Manifest {
	t.Helper()
	faucet := true
	m, err := Publish(PublishInput{
		Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis, ReleaseRoot: testRoot,
		Seeds: []string{"seed1.teststage.example.org"}, Channel: ChannelNightly, MinVersion: "0.3.0",
		ReleaseRepo: "https://releases.example.org/", Faucet: &faucet,
	})
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	return m
}

func TestPublish_writesAVerifiableNetwork(t *testing.T) {
	dir := t.TempDir()
	m := firstPublish(t, dir)
	if m.GenesisSHA256 != Digest(testGenesis) || m.ReleaseRootSHA256 != Digest(testRoot) {
		t.Errorf("digests = %s %s", m.GenesisSHA256, m.ReleaseRootSHA256)
	}
	n, err := loadNetworkDir(os.DirFS(filepath.Join(dir, "teststage")), ".", "teststage")
	if err != nil {
		t.Fatalf("what Publish wrote does not load: %v", err)
	}
	genesis, err := os.ReadFile(filepath.Join(dir, "teststage", GenesisFile))
	if err != nil || n.Manifest.VerifyGenesis(genesis) != nil {
		t.Errorf("published genesis does not verify: %v", err)
	}
}

func TestPublish_newChainIDReplacesTheNetworkAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	firstPublish(t, dir)
	next := []byte(`{"chain_id":"orama-teststage-2"}`)
	m, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-2", Genesis: next})
	if err != nil {
		t.Fatalf("a reset with a new chain id was refused: %v", err)
	}
	if m.ChainID != "orama-teststage-2" || m.GenesisSHA256 != Digest(next) {
		t.Errorf("manifest = %+v", m)
	}
	if m.Seeds[0] != "seed1.teststage.example.org" || !m.Faucet || m.ReleaseRootSHA256 != Digest(testRoot) {
		t.Errorf("a reset lost the rest of the manifest: %+v", m)
	}
}

func TestPublish_refusesToChangeAPublishedGenesis(t *testing.T) {
	dir := t.TempDir()
	firstPublish(t, dir)
	changed := []byte(`{"chain_id":"orama-teststage-1","app_state":{"changed":true}}`)
	_, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: changed})
	if !errors.Is(err, ErrGenesisChange) {
		t.Fatalf("Publish = %v, want ErrGenesisChange", err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "teststage", GenesisFile))
	if string(onDisk) != string(testGenesis) {
		t.Error("a refused publish still changed genesis.json")
	}
}

func TestPublish_sameGenesisAgainIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first := firstPublish(t, dir)
	again, err := Publish(PublishInput{Dir: dir, Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis})
	if err != nil || again.GenesisSHA256 != first.GenesisSHA256 || again.ChainID != first.ChainID {
		t.Fatalf("republishing the same genesis = %v, %v", again, err)
	}
}

func TestPublish_inputErrors(t *testing.T) {
	for name, in := range map[string]PublishInput{
		"genesis is for another chain": {Name: "teststage", ChainID: "orama-teststage-9", Genesis: testGenesis, ReleaseRoot: testRoot},
		"genesis is not JSON":          {Name: "teststage", ChainID: "orama-teststage-1", Genesis: []byte("x"), ReleaseRoot: testRoot},
		"no release root anywhere":     {Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis},
		"first publish without seeds":  {Name: "teststage", ChainID: "orama-teststage-1", Genesis: testGenesis, ReleaseRoot: testRoot},
	} {
		t.Run(name, func(t *testing.T) {
			in.Dir = t.TempDir()
			if _, err := Publish(in); err == nil {
				t.Fatal("Publish accepted it")
			}
			if entries, _ := os.ReadDir(in.Dir); len(entries) != 0 {
				t.Errorf("a refused publish left %d entries behind", len(entries))
			}
		})
	}
}
