package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"gopkg.in/yaml.v3"
)

// An upgrade regenerates node.yaml. The faucet an operator turned on must survive, or the
// newcomers sent to this node's gateway find none at the next upgrade.
func TestChainFaucetKeyFile_carriedForwardFromNodeYAML(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		want string
	}{
		"on, the default file": {"chain:\n  faucet:\n    enabled: true\n", constants.ChainFaucetKeyFile},
		"on, its own file":     {"chain:\n  faucet:\n    enabled: true\n    key_file: /etc/orama/faucet.key\n", "/etc/orama/faucet.key"},
		"off":                  {"chain:\n  faucet:\n    enabled: false\n", ""},
		"no chain block":       {"node:\n  id: x\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeACMENodeYAML(t, dir, tc.yaml)
			got, err := NewConfigGenerator(dir).ChainFaucetKeyFile()
			if err != nil || got != tc.want {
				t.Fatalf("ChainFaucetKeyFile = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRegeneratedNodeYAML_keepsTheFaucetAndStillParses(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "node:\n  id: x\nhttp_gateway:\n  base_domain: stagenet.orama.network\nchain:\n  faucet:\n    enabled: true\n    key_file: /etc/orama/faucet.key\n")
	rendered, err := NewConfigGenerator(dir).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.orama.network", "stagenet.orama.network", false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("regenerated node.yaml does not parse as config.Config: %v\n%s", err, rendered)
	}
	if !cfg.Chain.Faucet.Enabled || cfg.Chain.Faucet.KeyFile != "/etc/orama/faucet.key" {
		t.Fatalf("the faucet was dropped by the regeneration: %+v\n%s", cfg.Chain.Faucet, rendered)
	}
	for _, e := range cfg.Validate() {
		if strings.Contains(e.Error(), "chain.faucet") {
			t.Errorf("the regenerated faucet fails validation: %v", e)
		}
	}
}

func TestNodeYAML_withoutAFaucetHasNoChainBlock(t *testing.T) {
	rendered, err := NewConfigGenerator(t.TempDir()).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.example", "stagenet.example", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "faucet") {
		t.Fatalf("a node with no faucet renders one:\n%s", rendered)
	}
}

func TestChainFaucetKeyFile_aBrokenOrEditedFileIsAnErrorNotAnOffSwitch(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "chain: [unclosed\n")
	if _, err := NewConfigGenerator(dir).ChainFaucetKeyFile(); err == nil || !strings.Contains(err.Error(), "chain.faucet") {
		t.Fatalf("an unreadable node.yaml must be an error, got %v", err)
	}
	for _, bad := range []string{
		"chain:\n  faucet:\n    key_file: /x\n",
		"chain:\n  faucet:\n    enabled: true\n    key_file: relative.key\n",
		"chain:\n  faucet:\n    enabled: true\n    key_file: /a/../b\n",
	} {
		dir := t.TempDir()
		writeACMENodeYAML(t, dir, bad)
		if _, err := NewConfigGenerator(dir).ChainFaucetKeyFile(); err == nil {
			t.Errorf("%q was carried forward", bad)
		}
	}
}

// The regeneration that would write a block the node does not start with fails first.
func TestGenerateNodeConfig_refusesAFaucetBlockTheNodeWouldNotStartWith(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "chain:\n  faucet:\n    enabled: true\n    key_file: faucet.key\n")
	if _, err := NewConfigGenerator(dir).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.example", "stagenet.example", false); err == nil {
		t.Fatal("a regeneration wrote a faucet key file the node would refuse")
	}
}
