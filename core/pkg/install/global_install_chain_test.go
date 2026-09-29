package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstallGlobal_initChainRunsInitAsTheChainAccount(t *testing.T) {
	f := newGlobalFixture(t)
	f.freshHome(t)
	genesis := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(genesis, []byte(`{"chain_id":"orama-test-1","app_state":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := f.options(GlobalServiceChain)
	opts.InitChain = &ChainInit{ChainID: "orama-test-1", Moniker: "node-a", GenesisPath: genesis}
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	want := "-u orama-chain -- " + filepath.Join(f.host.BinDir, "oramad") + " init node-a --chain-id orama-test-1 --default-denom norama --home " + f.host.ChainHome
	if got := f.node.named("runuser"); !slices.Equal(got, []string{want}) {
		t.Fatalf("runuser = %v, want %q", got, want)
	}
	installed, err := os.ReadFile(filepath.Join(f.host.ChainHome, "config", "genesis.json"))
	if err != nil || !strings.Contains(string(installed), "orama-test-1") {
		t.Fatalf("genesis = %q (%v), want the network genesis", installed, err)
	}
	if !slices.Contains(f.chowns, chownCall{f.host.ChainHome, 990, 991}) {
		t.Error("the chain home was not given to the chain account")
	}
}

func TestInstallGlobal_initChainRefusesAnInitialisedHome(t *testing.T) {
	f := newGlobalFixture(t)
	genesis := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(genesis, []byte(`{"chain_id":"orama-test-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.host.ChainHome, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.host.ChainHome, "config", "genesis.json"), []byte(`{"chain_id":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := f.options(GlobalServiceChain)
	opts.InitChain = &ChainInit{ChainID: "orama-test-1", Moniker: "node-a", GenesisPath: genesis}
	err := InstallGlobal(opts, f.host)
	if err == nil || !strings.Contains(err.Error(), "never re-initialises") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if f.node.named("runuser") != nil {
		t.Error("oramad init ran over an existing home")
	}
}

func TestInstallGlobal_initChainRefusesAGenesisForAnotherChain(t *testing.T) {
	f := newGlobalFixture(t)
	f.freshHome(t)
	genesis := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(genesis, []byte(`{"chain_id":"orama-other-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := f.options(GlobalServiceChain)
	opts.InitChain = &ChainInit{ChainID: "orama-test-1", Moniker: "node-a", GenesisPath: genesis}
	if err := InstallGlobal(opts, f.host); err == nil || !strings.Contains(err.Error(), "orama-other-1") {
		t.Fatalf("err = %v, want the chain id mismatch", err)
	}
}
