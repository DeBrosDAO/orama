package config

import "testing"

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestFromEnv_fleetMode(t *testing.T) {
	m, err := FromEnv(env(map[string]string{EnvState: " /tmp/s.json ", EnvStrict: "1"}))
	if err != nil || m.StatePath != "/tmp/s.json" || !m.Strict {
		t.Fatalf("m=%+v err=%v", m, err)
	}
}

func TestFromEnv_unsetIsNonStrict(t *testing.T) {
	m, err := FromEnv(env(nil))
	if err != nil || m.StatePath != "" || m.Strict {
		t.Fatalf("m=%+v err=%v", m, err)
	}
}

func TestFromEnv_badStrictValue(t *testing.T) {
	if _, err := FromEnv(env(map[string]string{EnvStrict: "maybe"})); err == nil {
		t.Fatal("E2E_STRICT=maybe accepted")
	}
}

func TestBool_values(t *testing.T) {
	cases := map[string]bool{"1": true, "TRUE": true, "yes": true, "0": false, "false": false, "No": false}
	for v, want := range cases {
		got, err := Bool(env(map[string]string{"X": v}), "X", !want)
		if err != nil || got != want {
			t.Errorf("%q: got %v err %v", v, got, err)
		}
	}
	if got, err := Bool(env(map[string]string{"X": "  "}), "X", true); err != nil || !got {
		t.Errorf("blank must be default: got %v err %v", got, err)
	}
}

func TestCheckRunName_cases(t *testing.T) {
	for _, bad := range []string{"testnet", "e2e-TestNet-1", "mainnet.example"} {
		if err := CheckRunName("run id", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"e2e-ab12", "dbrsteting.bid", ""} {
		if err := CheckRunName("zone", ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func TestCheckRunName_sharedEnvironments(t *testing.T) {
	for _, bad := range []string{"devnet", "e2e-StageNet"} {
		if err := CheckRunName("environment", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCheckZone_onlyAllowedZone(t *testing.T) {
	for _, ok := range []string{"dbrsteting.bid", "DBRSTETING.BID.", " dbrsteting.bid "} {
		if err := CheckZone(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "orama.network", "dbrsteting.bid.evil.com", "x.dbrsteting.bid", "dbrsteting.bidx"} {
		if err := CheckZone(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCheckEnvName_mustBeRunEnvironment(t *testing.T) {
	if err := CheckEnvName("e2e-ab12"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "devnet", "sandbox", "e2e-devnet", "prod-e2e-ab12"} {
		if err := CheckEnvName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCheckBaseDomain_underAllowedZone(t *testing.T) {
	if err := CheckBaseDomain("e2e-ab12.dbrsteting.bid"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "dbrsteting.bid", "e2e-ab12.orama.network", "ab12.dbrsteting.bid", "e2e-ab12.x.dbrsteting.bid", "e2e-testnet.dbrsteting.bid"} {
		if err := CheckBaseDomain(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCheckChainID_runChainOnly(t *testing.T) {
	for _, ok := range []string{"", "orama-devnet-e2e-ab12"} {
		if err := CheckChainID(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"orama-devnet-1", "orama-testnet-e2e-ab12", "orama-mainnet-e2e-x", "orama-stagenet-e2e-x"} {
		if err := CheckChainID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
