package constants_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestLocalChainRPCURL_loopbackRPCPort(t *testing.T) {
	if got, want := constants.LocalChainRPCURL(), "http://127.0.0.1:31001"; got != want {
		t.Errorf("LocalChainRPCURL() = %q, want %q", got, want)
	}
	if got, want := constants.LocalChainAPIURL(), "http://127.0.0.1:31003"; got != want {
		t.Errorf("LocalChainAPIURL() = %q, want %q", got, want)
	}
}

// deploy.sh writes the chain's listen addresses; the constants only describe
// them. A port changed on one side and not the other leaves the node report
// probing a closed port, so the two are compared here.
func TestChainPorts_matchStagenetDeployScript(t *testing.T) {
	script := filepath.Join("..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	for name, want := range map[string]int{
		"P2P_PORT":  constants.ChainP2PPort,
		"RPC_PORT":  constants.ChainRPCPort,
		"GRPC_PORT": constants.ChainGRPCPort,
		"API_PORT":  constants.ChainAPIPort,
		"PROM_PORT": constants.ChainPrometheusPort,
	} {
		m := regexp.MustCompile(`(?m)^` + name + `=([0-9]+)$`).FindSubmatch(data)
		if m == nil {
			t.Errorf("%s does not set %s", script, name)
			continue
		}
		if got, _ := strconv.Atoi(string(m[1])); got != want {
			t.Errorf("%s: deploy.sh has %d, constants have %d", name, got, want)
		}
	}
	unit := regexp.MustCompile(`(?m)^UNIT="([^"]+)"$`).FindSubmatch(data)
	if unit == nil || string(unit[1]) != constants.ChainServiceUnit {
		t.Errorf("deploy.sh unit = %q, constants have %q", unit, constants.ChainServiceUnit)
	}
}

// The chain block must not overlap the index or tenant blocks, whose
// allocators hand ports out without knowing about the chain.
func TestChainPorts_outsideIndexAndTenantBlocks(t *testing.T) {
	for _, p := range []int{constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort,
		constants.ChainAPIPort, constants.ChainPrometheusPort} {
		if p >= 10000 && p <= constants.IndexPortEnd {
			t.Errorf("chain port %d lies in the tenant/index range", p)
		}
	}
}
