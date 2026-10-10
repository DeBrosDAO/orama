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
	// ManifestSHA256 is the digest of /opt/orama/manifest.json, the build the
	// machine runs; empty when there is none.
	ManifestSHA256 string
}

// probeScript prints Facts as key=value lines. Each value comes from a command
// whose failure is an empty value, never an error: a fresh machine has none of
// these files. The hardware lines are install.HardwareProbeCommand.
var probeScript = strings.Join([]string{
	install.HardwareProbeCommand,
	`echo arch=$(uname -m)`,
	`[ -f ` + unitDir + `/orama-node.service ] && echo cluster=1 || echo cluster=0`,
	`[ -f ` + unitDir + `/` + constants.ChainServiceUnit + ` ] && echo global=1 || echo global=0`,
	`echo manifest_sha=$(sha256sum /opt/orama/manifest.json 2>/dev/null | cut -d' ' -f1)`,
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
	f := Facts{Arch: arch, Hardware: hw, ClusterInstalled: kv["cluster"] == "1", GlobalInstalled: kv["global"] == "1"}
	if v := kv["manifest_sha"]; v != "" {
		if !sha256Hex.MatchString(v) {
			return Facts{}, fmt.Errorf("probe: manifest_sha=%q is not a SHA-256", v)
		}
		f.ManifestSHA256 = v
	}
	return f, nil
}
