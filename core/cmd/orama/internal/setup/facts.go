package setup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// Facts are what a machine is and already has, read before anything on it
// changes. A step whose result is already there is not run again.
type Facts struct {
	// Arch is the machine's architecture as Go names it.
	Arch     string
	Hardware install.Hardware
	// ClusterInstalled: orama-node.service is installed.
	ClusterInstalled bool
	// GlobalInstalled: the chain unit is installed.
	GlobalInstalled bool
	// ChainActive: the chain unit is running.
	ChainActive bool
	// ManifestSHA256 is the digest of /opt/orama/manifest.json, the build the
	// machine runs; empty when there is none. CLISHA256 is the digest of its
	// bin/orama: a manifest alone says nothing about the binary beside it.
	ManifestSHA256 string
	CLISHA256      string
	// RestartPending: the chain unit was installed after the cluster node last
	// started, so the cluster gateway has not read the chain's listeners yet.
	RestartPending bool
}

// probeScript prints Facts as key=value lines. Each value comes from a command
// whose failure is an empty value, never an error: a fresh machine has none of
// these files. The hardware lines are install.HardwareProbeCommand.
var probeScript = strings.Join([]string{
	install.HardwareProbeCommand,
	`echo arch=$(uname -m)`,
	`[ -f ` + unitDir + `/orama-node.service ] && echo cluster=1 || echo cluster=0`,
	`[ -f ` + unitDir + `/` + constants.ChainServiceUnit + ` ] && echo global=1 || echo global=0`,
	`systemctl is-active --quiet ` + constants.ChainServiceUnit + ` && echo chain_active=1 || echo chain_active=0`,
	`echo manifest_sha=$(sha256sum /opt/orama/manifest.json 2>/dev/null | cut -d' ' -f1)`,
	`echo cli_sha=$(sha256sum /opt/orama/bin/orama 2>/dev/null | cut -d' ' -f1)`,
	// The node's last start, and the chain unit's file time: the gateway reads the
	// chain's listeners when it starts, so a unit newer than the start is not seen.
	`ts=$(systemctl show orama-node.service -p ActiveEnterTimestamp --value 2>/dev/null); ` +
		`if [ -n "$ts" ]; then started=$(date -d "$ts" +%s 2>/dev/null || echo 0); else started=0; fi; ` +
		`unit=$(stat -c %Y ` + unitDir + `/` + constants.ChainServiceUnit + ` 2>/dev/null || echo 0); ` +
		`if [ "$unit" -gt "$started" ]; then echo restart_pending=1; else echo restart_pending=0; fi`,
}, "; ")

// unitDir is where the cluster node's and the chain's units are installed.
const unitDir = constants.SystemdUnitDir

// goArch names the architectures `uname -m` reports as Go does.
var goArch = map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseFacts reads probeScript's output.
func ParseFacts(out string) (Facts, error) {
	hw, err := install.ParseHardware(out)
	if err != nil {
		return Facts{}, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	arch, ok := goArch[kv["arch"]]
	if !ok {
		return Facts{}, fmt.Errorf("the machine reports architecture %q, which no release is built for", kv["arch"])
	}
	f := Facts{
		Arch: arch, Hardware: hw, ClusterInstalled: kv["cluster"] == "1", GlobalInstalled: kv["global"] == "1",
		ChainActive: kv["chain_active"] == "1", RestartPending: kv["restart_pending"] == "1",
	}
	for key, dst := range map[string]*string{"manifest_sha": &f.ManifestSHA256, "cli_sha": &f.CLISHA256} {
		if v := kv[key]; v != "" {
			if !sha256Hex.MatchString(v) {
				return Facts{}, fmt.Errorf("probe: %s=%q is not a SHA-256", key, v)
			}
			*dst = v
		}
	}
	return f, nil
}
