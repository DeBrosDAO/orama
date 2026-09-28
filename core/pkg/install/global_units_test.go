package install

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestGlobalUnits_usersPortsAndNoClusterSecret(t *testing.T) {
	chain := RenderGlobalChainUnit()
	ipfs := RenderGlobalIPFSUnit()
	relay := RenderGlobalRelayUnit()

	users := []string{}
	for _, unit := range []string{chain, ipfs, relay} {
		user := mustDirective(t, unit, "User")
		group := mustDirective(t, unit, "Group")
		if user != group {
			t.Errorf("User=%s Group=%s", user, group)
		}
		if user == "orama" || user == "" {
			t.Errorf("global unit runs as %q", user)
		}
		users = append(users, user)
		if strings.Contains(unit, "cluster-secret") || strings.Contains(unit, "/opt/orama/.orama/secrets") {
			t.Errorf("unit contains a cluster secret path:\n%s", unit)
		}
		if strings.Contains(unit, "PartOf=") {
			t.Errorf("global unit is PartOf another unit:\n%s", unit)
		}
		for _, port := range []string{":10100", ":10102", ":10103", ":10104", ":10107"} {
			if strings.Contains(unit, port) {
				t.Errorf("unit contains cluster port %s", port)
			}
		}
	}
	if users[0] == users[1] || users[1] == users[2] || users[0] == users[2] {
		t.Fatalf("global units share users %v", users)
	}
	if users[0] != globalChainUser || users[1] != globalIPFSUser || users[2] != globalRelayUser {
		t.Fatalf("users = %v, want %s %s %s", users, globalChainUser, globalIPFSUser, globalRelayUser)
	}

	for _, p := range []int{
		constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort,
		constants.ChainAPIPort, constants.ChainPrometheusPort,
	} {
		if !strings.Contains(chain, strconv.Itoa(p)) {
			t.Errorf("chain unit missing port %d\n%s", p, chain)
		}
	}
	for _, want := range []string{
		"tcp://0.0.0.0:31000",
		"tcp://127.0.0.1:31001",
		"127.0.0.1:31002",
		"tcp://127.0.0.1:31003",
		"127.0.0.1:31004",
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("chain unit missing %s", want)
		}
	}
	ipfsAPI := "/ip4/127.0.0.1/tcp/" + strconv.Itoa(constants.GlobalIPFSAPIPort)
	if !strings.Contains(ipfs, ipfsAPI) || !strings.Contains(ipfs, "127.0.0.1") {
		t.Errorf("ipfs unit missing loopback API %s\n%s", ipfsAPI, ipfs)
	}
	if constants.GlobalIPFSAPIPort != 31107 {
		t.Fatalf("GlobalIPFSAPIPort = %d", constants.GlobalIPFSAPIPort)
	}
	relayAddr := "127.0.0.1:" + strconv.Itoa(constants.GlobalRelayMetricsPort)
	if !strings.Contains(relay, relayAddr) {
		t.Errorf("relay unit missing %s\n%s", relayAddr, relay)
	}
	if constants.GlobalRelayMetricsPort != 31110 {
		t.Fatalf("GlobalRelayMetricsPort = %d", constants.GlobalRelayMetricsPort)
	}
}

func TestGlobalChainUnit_userMatchesStagenetDeploy(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	m := regexp.MustCompile(`(?m)^SVC_USER="([^"]+)"$`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s does not set SVC_USER", script)
	}
	if string(m[1]) != globalChainUser {
		t.Fatalf("deploy.sh user %q, template user %q", m[1], globalChainUser)
	}
	unit := RenderGlobalChainUnit()
	if !strings.Contains(unit, "User="+globalChainUser) {
		t.Fatalf("chain unit user is not %s", globalChainUser)
	}
}

func mustDirective(t *testing.T, unit, key string) string {
	t.Helper()
	var found string
	n := 0
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, key+"=") {
			found = strings.TrimPrefix(line, key+"=")
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%s appears %d times in\n%s", key, n, unit)
	}
	return found
}
