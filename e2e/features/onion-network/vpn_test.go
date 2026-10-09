//go:build e2e_fleet

package onionnetwork

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// A well-formed validator onion address (56 base32 characters).
const sampleOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

// networkFile writes a network file (tor-network.json, the one format every
// role and client reads) whose authorities are public-looking addresses: valid,
// and nobody answers there.
func networkFile(t *testing.T, private bool) string {
	t.Helper()
	n := tornet.Network{
		Name: "e2e", Private: private, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300,
		ValidatorOnions: []string{sampleOnion},
	}
	for i, ip := range []string{"192.5.5.241", "198.41.0.4", "199.7.91.13"} {
		n.Authorities = append(n.Authorities, tornet.Authority{
			Nickname: fmt.Sprintf("E2EAuth%d", i+1), Address: ip, ORPort: 31020, DirPort: 31021,
			V3Ident: fmt.Sprintf("%040X", 0xA0+i), Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		})
	}
	// Marshal refuses a network that is not private, and the refusal is what
	// the tests probe, so the file is written without it.
	body, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), constants.TorNetworkFile)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// liveNetwork is the network file of the live private Tor network, or the
// test is not applicable.
func liveNetwork(t *testing.T) string {
	t.Helper()
	path := os.Getenv(tornet.NetworkEnv)
	if path == "" {
		harness.SkipNotApplicable(t, tornet.NetworkEnv+" names no network file: the run has no private Tor network to join (track E builds it)")
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
	cli := harness.CLI(t)
	cli.Env = []string{tornet.NetworkEnv + "=" + liveNetwork(t)}
	res := infra.RunFor(t, cli, checkBudget, "vpn", "check")
	infra.ExpectExit(t, res, infra.ExitOK, "Joined the", "answered through the network")
}

// The variable names the same file as --network, through the same parser: a
// public network file is refused whichever way it is named. The CLI inherits
// nothing from the test process, so the variable is set on the runner.
func TestVPN_theNetworkFileCanComeFromTheEnvironment(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cli.Env = []string{tornet.NetworkEnv + "=" + networkFile(t, false)}
	for _, verb := range []string{"up", "check"} {
		res := infra.Run(t, cli, "vpn", verb)
		infra.ExpectExit(t, res, infra.ExitUsage, "not launched")
	}
}
