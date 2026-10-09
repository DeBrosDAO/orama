//go:build e2e_fleet

package onionnetwork

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// envNetwork names the network file of a live private Tor network (the
// stagenet one) for the tests that need to join it.
const envNetwork = "E2E_ONION_NETWORK"

// A well-formed validator onion address (56 base32 characters).
const sampleOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

// networkFile writes a network file whose authorities are documentation
// addresses: valid, and nobody answers there.
func networkFile(t *testing.T, private bool) string {
	t.Helper()
	type authority struct {
		Nickname    string `json:"nickname"`
		Address     string `json:"address"`
		ORPort      int    `json:"orport"`
		V3Ident     string `json:"v3ident"`
		Fingerprint string `json:"fingerprint"`
	}
	doc := struct {
		Name            string      `json:"name"`
		Private         bool        `json:"private"`
		Authorities     []authority `json:"authorities"`
		ValidatorOnions []string    `json:"validator_onions"`
	}{Name: "e2e", Private: private, ValidatorOnions: []string{sampleOnion}}
	for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		doc.Authorities = append(doc.Authorities, authority{
			Nickname: "e2eauth" + string(rune('a'+i)), Address: ip + ":31021", ORPort: 31020,
			V3Ident: strings.Repeat(string(rune('a'+i)), 40), Fingerprint: strings.Repeat("0", 40),
		})
	}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// liveNetwork is the network file of the live private Tor network, or the
// test is not applicable.
func liveNetwork(t *testing.T) string {
	t.Helper()
	path := os.Getenv(envNetwork)
	if path == "" {
		harness.SkipNotApplicable(t, envNetwork+" names no network file: the run has no private Tor network to join (track E builds it)")
	}
	return path
}

func TestVPN_groupListsItsCommands(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, harness.CLI(t), "vpn")
	infra.ExpectExit(t, res, infra.ExitOK, "up", "check")
}

func TestVPN_aPublicNetworkIsNotJoined(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	for _, verb := range []string{"up", "check"} {
		res := infra.Run(t, cli, "vpn", verb, "--network", networkFile(t, false))
		infra.ExpectExit(t, res, infra.ExitUsage, "not launched")
	}
}

func TestVPN_aNetworkIsRequired(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, harness.CLI(t), "vpn", "up")
	infra.ExpectExit(t, res, infra.ExitUsage, "--network")
}

func TestVPN_aMissingTorBinaryIsAnError(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, harness.CLI(t), "vpn", "up", "--network", networkFile(t, true), "--tor", "/nonexistent/tor", "--data-dir", t.TempDir()+"/tor")
	infra.ExpectExit(t, res, infra.ExitUnavailable, "start tor")
}

func TestVPN_theProxyStaysOnLoopback(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, harness.CLI(t), "vpn", "up", "--network", networkFile(t, true), "--socks", "0.0.0.0:9150", "--tor", "/nonexistent/tor", "--data-dir", t.TempDir()+"/tor")
	infra.ExpectRefused(t, res, "loopback")
}

func TestVPN_checkReachesAValidatorThroughTheNetwork(t *testing.T) {
	file := liveNetwork(t)
	res := infra.RunFor(t, harness.CLI(t), checkBudget, "vpn", "check", "--network", file)
	infra.ExpectExit(t, res, infra.ExitOK, "Joined the", "answered through the network")
}
