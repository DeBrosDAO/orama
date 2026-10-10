package chainread

import "testing"

func TestConfigFor_defaultsToLoopbackOffACoLocatedMachine(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	got := configFor(false)
	if got.RPCURL != "http://127.0.0.1:31001" || got.RESTURL != "http://127.0.0.1:31003" || got.IndexURL != "http://127.0.0.1:31015" {
		t.Errorf("config = %+v", got)
	}
}

func TestConfigFor_pointsAtTheNamespaceAddressOnACoLocatedMachine(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	got := configFor(true)
	if got.RPCURL != "http://198.18.0.2:31001" || got.RESTURL != "http://198.18.0.2:31003" || got.IndexURL != "http://198.18.0.2:31015" {
		t.Errorf("config = %+v", got)
	}
}

func TestConfigFor_anEnvironmentValueWinsOverTheCoLocatedDefault(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "http://10.9.9.9:1")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	got := configFor(true)
	if got.RPCURL != "http://10.9.9.9:1" || got.RESTURL != "http://198.18.0.2:31003" {
		t.Errorf("config = %+v", got)
	}
}

func TestConfigFromEnv_usesTheMachinesLayout(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	old := colocated
	defer func() { colocated = old }()
	colocated = func() bool { return true }
	if got := ConfigFromEnv().RPCURL; got != "http://198.18.0.2:31001" {
		t.Errorf("co-located RPC URL = %q", got)
	}
	colocated = func() bool { return false }
	if got := ConfigFromEnv().RPCURL; got != "http://127.0.0.1:31001" {
		t.Errorf("RPC URL = %q", got)
	}
}

func TestConfigFor_theIndexerIsAskedOnlyWhereItsUnitIsInstalled(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	old := indexerInstalled
	defer func() { indexerInstalled = old }()
	indexerInstalled = func() bool { return false }
	cfg := configFor(false)
	if cfg.IndexInstalled == nil || cfg.IndexInstalled() {
		t.Errorf("a machine without the indexer unit reports it installed")
	}
	// An index URL from the environment names an indexer the unit directory says nothing about.
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "http://10.9.9.9:31015")
	if cfg := configFor(false); cfg.IndexInstalled != nil {
		t.Errorf("an indexer named by ORAMA_CHAIN_INDEX_URL is checked against the unit directory")
	}
}
