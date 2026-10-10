package config

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestChainConfig_decodesTheFaucetBlock(t *testing.T) {
	var cfg Config
	err := DecodeStrict(strings.NewReader("chain:\n  faucet:\n    enabled: true\n    key_file: /opt/orama/.orama/secrets/f.key\n"), &cfg)
	if err != nil || !cfg.Chain.Faucet.Enabled || cfg.Chain.Faucet.KeyFile != "/opt/orama/.orama/secrets/f.key" {
		t.Fatalf("faucet %+v err %v", cfg.Chain.Faucet, err)
	}
	var none Config
	if err := DecodeStrict(strings.NewReader("node:\n  id: n\n"), &none); err != nil || none.Chain.Faucet.Enabled {
		t.Fatalf("a node.yaml with no chain block: %+v err %v", none.Chain, err)
	}
	if err := DecodeStrict(strings.NewReader("chain:\n  faucet:\n    enabled: true\n    drip: 5\n"), &cfg); err == nil {
		t.Error("an unknown key under chain.faucet was accepted: a misspelt setting must not leave the default in place")
	}
}

func TestFaucetConfig_keyFilePath(t *testing.T) {
	for name, tc := range map[string]struct {
		in   FaucetConfig
		want string
	}{
		"off":                  {FaucetConfig{}, ""},
		"off with a key file":  {FaucetConfig{KeyFile: "/x"}, ""},
		"on, the default file": {FaucetConfig{Enabled: true}, constants.ChainFaucetKeyFile},
		"on, its own file":     {FaucetConfig{Enabled: true, KeyFile: "/etc/f.key"}, "/etc/f.key"},
	} {
		if got := tc.in.KeyFilePath(); got != tc.want {
			t.Errorf("%s: KeyFilePath = %q, want %q", name, got, tc.want)
		}
	}
}

func TestConfigValidate_faucet(t *testing.T) {
	cfg := validConfigForNode()
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("a config with no faucet: %v", errs)
	}
	for name, f := range map[string]FaucetConfig{
		"on with the default file": {Enabled: true},
		"on with its own file":     {Enabled: true, KeyFile: "/etc/orama/faucet.key"},
	} {
		cfg.Chain.Faucet = f
		if errs := cfg.Validate(); len(errs) != 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
	for name, f := range map[string]FaucetConfig{
		"a key file with the faucet off": {KeyFile: "/etc/orama/faucet.key"},
		"a relative key file":            {Enabled: true, KeyFile: "faucet.key"},
		"an unclean key file":            {Enabled: true, KeyFile: "/etc/orama/../faucet.key"},
		"a quote in the key file":        {Enabled: true, KeyFile: `/etc/orama/"x.key`},
		"a newline in the key file":      {Enabled: true, KeyFile: "/etc/orama/x\nfaucet.key"},
		"a space in the key file":        {Enabled: true, KeyFile: "/etc/orama/my faucet.key"},
	} {
		cfg.Chain.Faucet = f
		errs := cfg.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "chain.faucet.key_file") {
			t.Errorf("%s: errs = %v", name, errs)
		}
	}
}
