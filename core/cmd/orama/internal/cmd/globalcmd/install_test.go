package globalcmd

import "testing"

func TestCheckChainClientUsers(t *testing.T) {
	if err := checkChainClientUsers(nil, false); err != nil {
		t.Errorf("no users: %v", err)
	}
	if err := checkChainClientUsers([]string{"debian"}, true); err != nil {
		t.Errorf("a named user with --colocated: %v", err)
	}
	for name, users := range map[string][]string{"empty": {""}, "blank": {"  "}, "one empty among others": {"debian", ""}} {
		if err := checkChainClientUsers(users, true); err == nil {
			t.Errorf("%s: an empty --chain-client-user was accepted", name)
		}
	}
	if err := checkChainClientUsers([]string{"debian"}, false); err == nil {
		t.Error("--chain-client-user without --colocated was accepted")
	}
}

func withChainFlags(t *testing.T, address string, servers []string, height int64, hash string) {
	t.Helper()
	old := installFlags
	t.Cleanup(func() { installFlags = old })
	installFlags.externalAddress, installFlags.stateSyncRPC = address, servers
	installFlags.trustHeight, installFlags.trustHash = height, hash
}

func TestChainConfigFromFlags_noFlagsNoConfig(t *testing.T) {
	withChainFlags(t, "", nil, 0, "")
	c, err := chainConfigFromFlags()
	if err != nil || c != nil {
		t.Fatalf("got %+v, %v; want no config", c, err)
	}
}

func TestChainConfigFromFlags_servingNode(t *testing.T) {
	withChainFlags(t, "203.0.113.7:31000", nil, 0, "")
	c, err := chainConfigFromFlags()
	if err != nil || c == nil || c.StateSync != nil || c.ExternalAddress != "203.0.113.7:31000" {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestChainConfigFromFlags_joiner(t *testing.T) {
	hash := "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
	withChainFlags(t, "203.0.113.7:31000", []string{"https://a.example/v1/chain/light", "https://b.example/v1/chain/light"}, 5000, hash)
	c, err := chainConfigFromFlags()
	if err != nil || c.StateSync == nil || c.StateSync.TrustHeight != 5000 || len(c.StateSync.RPCServers) != 2 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestChainConfigFromFlags_refusals(t *testing.T) {
	hash := "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
	servers := []string{"https://a.example/v1/chain/light", "https://b.example/v1/chain/light"}
	for name, f := range map[string]func(){
		"state sync without an external address": func() { withChainFlags(t, "", servers, 5000, hash) },
		"servers without a trusted block":        func() { withChainFlags(t, "203.0.113.7:31000", servers, 0, "") },
		"a trusted block without servers":        func() { withChainFlags(t, "203.0.113.7:31000", nil, 5000, hash) },
		"a hostname as the address":              func() { withChainFlags(t, "seed.example:31000", nil, 0, "") },
	} {
		f()
		if _, err := chainConfigFromFlags(); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}
