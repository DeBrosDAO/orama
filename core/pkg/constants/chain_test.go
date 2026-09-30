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

// deploy.sh names the chain's peer-to-peer port and the provider's port: the
// first goes into the peers it builds and the second into the endpoint it
// registers. The listeners themselves come from `orama global install`, whose
// units use the constants. A port changed on one side and not the other
// leaves peers or clients dialling a closed port, so the two are compared here.
func TestChainPorts_matchStagenetDeployScript(t *testing.T) {
	script := filepath.Join("..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	for name, want := range map[string]int{
		"P2P_PORT":      constants.ChainP2PPort,
		"PROVIDER_PORT": constants.GlobalProviderPort,
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
}

func TestStagenetDeployScript_usesTheNamespaceAddress(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^NS_ADDR="` + regexp.QuoteMeta(constants.GlobalNetnsAddr) + `"$`).Match(data) {
		t.Errorf("deploy.sh NS_ADDR is not %s", constants.GlobalNetnsAddr)
	}
}

// The chain module cannot import core, so provider/kubo.go repeats the namespace address it
// trusts for the bearer token. A change on either side must fail here.
func TestProviderKubo_colocatedHostIsTheNamespaceAddress(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "chain", "provider", "kubo.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^const colocatedKuboHost = "([^"]*)"$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("chain/provider/kubo.go has no colocatedKuboHost constant")
	}
	if got := string(m[1]); got != constants.GlobalNetnsAddr {
		t.Errorf("colocatedKuboHost is %q, constants.GlobalNetnsAddr is %q", got, constants.GlobalNetnsAddr)
	}
}

func TestColocatedChainURLs_areTheNamespaceAddress(t *testing.T) {
	for got, want := range map[string]string{
		constants.ColocatedChainRPCURL():      "http://198.18.0.2:31001",
		constants.ColocatedChainAPIURL():      "http://198.18.0.2:31003",
		constants.ColocatedGlobalIndexerURL(): "http://198.18.0.2:31015",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
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

// reset-node.sh puts net.ipv4.ip_forward back from the record the installer writes; the two must
// name the same file.
func TestStagenetResetScript_restoresForwardingFromTheInstallersRecord(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "chain", "scripts", "stagenet", "remote", "reset-node.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := `PRIOR_FORWARD=` + constants.GlobalStateRoot + `/` + constants.GlobalNetnsPriorForwardFile
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `$`).Match(data) {
		t.Errorf("reset-node.sh does not read %s", want)
	}
	if !regexp.MustCompile(`sysctl -q -w "net\.ipv4\.ip_forward=\$prior"`).Match(data) {
		t.Errorf("reset-node.sh does not restore net.ipv4.ip_forward from the record")
	}
}
