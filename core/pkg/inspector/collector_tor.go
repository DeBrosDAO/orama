package inspector

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// TorUnit is the node's client-only Tor daemon, run by the supervisor.
const TorUnit = "orama-namespace-tor@index"

// TorBootstrapUnknown is BootstrapPct when the running Tor process has no
// "Bootstrapped" line left in the journal (it was vacuumed).
const TorBootstrapUnknown = -1

// TorData holds the Tor client's status on a node.
type TorData struct {
	ClientActive   bool // orama-namespace-tor@index is active
	SocksListening bool // the Tor SOCKS port is bound
	BootstrapPct   int  // last bootstrap percentage of the running process, or TorBootstrapUnknown
	Bootstrapped   bool // BootstrapPct reached 100
	LegacyAnyone   bool // the removed Anyone network is still on the node
}

// torCollectScript prints, separated by ===INSPECTOR_SEP===: the unit state,
// whether the SOCKS port is bound, the bootstrap percentage of the running
// process, and whether any Anyone leftovers remain.
//
// Bootstrap is read from the journal of the current invocation only, so a
// "Bootstrapped 100%" from before a restart cannot vouch for the process now
// running.
func torCollectScript() string {
	return fmt.Sprintf(`
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl is-active --quiet %[1]s && echo active || echo inactive
echo "$SEP"
ss -tln 2>/dev/null | grep -q ':%[2]d ' && echo yes || echo no
echo "$SEP"
INV=$(systemctl show -p InvocationID --value %[1]s 2>/dev/null)
BPCT=""
if [ -n "$INV" ]; then
  BPCT=$(sudo -n journalctl --no-pager -o cat _SYSTEMD_INVOCATION_ID="$INV" 2>/dev/null | grep -oP 'Bootstrapped \K[0-9]+' | tail -1)
fi
echo "${BPCT:-%[3]d}"
echo "$SEP"
(systemctl is-active --quiet orama-namespace-anyone-client@index || test -e /etc/anon || test -e /var/lib/anon || test -e /etc/apt/sources.list.d/anon.list) && echo yes || echo no
`, TorUnit, constants.TorSOCKSPort, TorBootstrapUnknown)
}

func collectTor(ctx context.Context, node Node) *TorData {
	res := RunSSH(ctx, node, torCollectScript())
	if !res.OK() && res.Stdout == "" {
		return nil
	}
	return parseTorOutput(res.Stdout)
}

func parseTorOutput(stdout string) *TorData {
	data := &TorData{BootstrapPct: TorBootstrapUnknown}
	parts := strings.Split(stdout, "===INSPECTOR_SEP===")
	if len(parts) > 1 {
		data.ClientActive = strings.TrimSpace(parts[1]) == "active"
	}
	if len(parts) > 2 {
		data.SocksListening = strings.TrimSpace(parts[2]) == "yes"
	}
	if len(parts) > 3 {
		data.BootstrapPct = parseIntDefault(strings.TrimSpace(parts[3]), TorBootstrapUnknown)
		data.Bootstrapped = data.BootstrapPct >= 100
	}
	if len(parts) > 4 {
		data.LegacyAnyone = strings.TrimSpace(parts[4]) == "yes"
	}
	return data
}
