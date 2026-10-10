//go:build e2e_fleet

package joinonecommand

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
)

// unusedIP is a public address no server of the run has (TEST-NET-3): the
// refusals below happen before any server is contacted, so nothing needs to
// answer there.
const unusedIP = "203.0.113.77"

// TestSetup_unattendedWithoutAHostKeyIsRefused: with --yes a machine needs its
// --host-key, and setup never trusts a host key it was not given; the refusal
// is a usage error that names the machine and the flag, before any connection.
func TestSetup_unattendedWithoutAHostKeyIsRefused(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--cluster-only", "--ip", unusedIP)
	infra.ExpectExit(t, res, infra.ExitUsage, "no --host-key for "+unusedIP, "--host-key SHA256:")
}

// TestSetup_aHostKeyThatIsNotAFingerprintIsRefused: a --host-key must be a
// SHA256:... fingerprint.
func TestSetup_aHostKeyThatIsNotAFingerprintIsRefused(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--cluster-only", "--ip", unusedIP, "--host-key", "MD5:aa:bb")
	infra.ExpectExit(t, res, infra.ExitUsage, "SHA256:")
}

// TestSetup_aFullNodeNeedsAName: a full node's name is its id on the chain, so
// an unattended run without --name is refused.
func TestSetup_aFullNodeNeedsAName(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectExit(t, res, infra.ExitUsage, "--name")
}

// TestSetup_theExitRelayNeedsTheWarningAcceptedAndARelay: --exit without --yes
// and without a terminal is refused with the warning, and an exit is a relay,
// so it cannot go with --no-relay.
func TestSetup_theExitRelayNeedsTheWarningAcceptedAndARelay(t *testing.T) {
	cli := isolatedCLI(t)
	withoutConsent := infra.Run(t, cli, "setup", "--exit", "--tor-network", "tor-network.json", "--name", "exit", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectRefused(t, withoutConsent, "exit relay", "abuse complaints")
	withoutRelay := infra.Run(t, cli, "setup", "--exit", "--no-relay", "--yes", "--name", "exit", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectExit(t, withoutRelay, infra.ExitUsage, "--no-relay")
}

// TestSetup_theRelayFlagsAreAlternatives: the Tor network file is the one the
// network pins or the operator's own (--tor-network), never both, and the
// relay flags belong to the global layer.
func TestSetup_theRelayFlagsAreAlternatives(t *testing.T) {
	cli := isolatedCLI(t)
	both := infra.Run(t, cli, "setup", "--yes", "--no-relay", "--tor-network", "tor-network.json", "--name", "relay", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectExit(t, both, infra.ExitUsage, "alternatives")
	clusterOnly := infra.Run(t, cli, "setup", "--yes", "--cluster-only", "--no-relay", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectExit(t, clusterOnly, infra.ExitUsage, "--cluster-only")
}

// TestSetup_aNameTheChainWouldRefuseIsRefusedBeforeAnyServerIsTouched: a full
// node's name is also the name it claims on the chain, so the chain's rules
// (3 to 32 characters, nothing reserved) apply to --name at once.
func TestSetup_aNameTheChainWouldRefuseIsRefusedBeforeAnyServerIsTouched(t *testing.T) {
	cli := isolatedCLI(t)
	for name, want := range map[string]string{"gateway": "reserved", "ab": "3 to 32"} {
		res := infra.Run(t, cli, "setup", "--yes", "--name", name, "--ip", unusedIP, "--host-key", "SHA256:abc")
		infra.ExpectExit(t, res, infra.ExitUsage, "--name", want)
	}
}

// TestSetup_clusterOnlyRefusesTheGlobalLayerFlags: --exit, --storage-gb and
// --tor-network belong to the global layer and cannot go with --cluster-only.
func TestSetup_clusterOnlyRefusesTheGlobalLayerFlags(t *testing.T) {
	cli := isolatedCLI(t)
	for _, flag := range [][]string{{"--storage-gb", "10"}, {"--tor-network", "tor-network.json"}} {
		args := append([]string{"setup", "--yes", "--cluster-only", "--ip", unusedIP, "--host-key", "SHA256:abc"}, flag...)
		infra.ExpectExit(t, infra.Run(t, cli, args...), infra.ExitUsage, "--cluster-only")
	}
}

// TestSetup_aPrivateAddressIsRefused: the machines are reached at a public
// IPv4 address, and a private one is a mistake to catch before anything runs.
func TestSetup_aPrivateAddressIsRefused(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--cluster-only", "--ip", "10.0.0.5", "--host-key", "SHA256:abc")
	infra.ExpectExit(t, res, infra.ExitUsage, "public")
}

// TestSetup_aNetworkGivenAsAURLIsRefused: a manifest URL is added with `orama
// network add`, which shows the release root's digest and asks for a
// confirmation; --network takes only the name.
func TestSetup_aNetworkGivenAsAURLIsRefused(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--cluster-only", "--network", "https://example.org/networks/x/manifest.json", "--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectRefused(t, res, "orama network add")
}
