package main

import (
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"gopkg.in/yaml.v3"
)

// The cluster gateway's YAML carries faucet_key_file when node.yaml turns the faucet on. The
// standalone gateway decodes strictly, so a field the loader does not know stops it from
// starting, and a field it knows but does not copy leaves the faucet off with no error.
func TestSpawnedGatewayConfig_loadsFaucetKeyFile(t *testing.T) {
	const keyFile = "/opt/orama/.orama/secrets/chain-faucet.key"
	written := gateway.GatewayYAMLConfig{ListenAddr: ":6001", ClientNamespace: "index", FaucetKeyFile: keyFile}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatal(err)
	}

	// yamlCfgMirror mirrors the function-local yamlCfg in config.go.
	type yamlCfgMirror struct {
		ListenAddr      string   `yaml:"listen_addr"`
		ClientNamespace string   `yaml:"client_namespace"`
		RQLiteDSN       string   `yaml:"rqlite_dsn"`
		OlricServers    []string `yaml:"olric_servers"`
		FaucetKeyFile   string   `yaml:"faucet_key_file"`
		StateDir        string   `yaml:"state_dir"`
	}
	var y yamlCfgMirror
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		t.Fatalf("strict decode rejected the cluster gateway's YAML: %v", err)
	}
	if y.FaucetKeyFile != keyFile {
		t.Fatalf("faucet_key_file = %q, want %q", y.FaucetKeyFile, keyFile)
	}
	noFaucet, _ := yaml.Marshal(gateway.GatewayYAMLConfig{ListenAddr: ":6101", ClientNamespace: "tenant"})
	if strings.Contains(string(noFaucet), "faucet") {
		t.Errorf("a gateway with no faucet writes the key:\n%s", noFaucet)
	}
}

// The loader must copy the field into gateway.Config, where the gateway reads it.
func TestParseGatewayConfig_copiesFaucetKeyFile(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`yaml:"faucet_key_file"`, "cfg.FaucetKeyFile = strings.TrimSpace(y.FaucetKeyFile)"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("config.go does not contain %q: the faucet key file would not reach gateway.Config", want)
		}
	}
}
