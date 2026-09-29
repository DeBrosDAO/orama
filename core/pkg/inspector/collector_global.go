package inspector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// collectGlobalNode reads the chain RPC, the slashing REST, and the global
// unit state over one SSH session. The node report on the machine does the
// same checks and also verifies the CometBFT node key; this path does not
// have that key, so a process that bound the loopback port can answer as the chain.
func collectGlobalNode(ctx context.Context, node Node) (*report.ChainReport, *report.GlobalReport) {
	res := RunSSH(ctx, node, globalCollectScript())
	if !res.OK() && res.Stdout == "" {
		return nil, nil
	}
	return parseGlobalCollect(res.Stdout, time.Now())
}

// chainCurlFailed is what the collection script prints in a chain section when the request failed.
const chainCurlFailed = "ORAMA_CHAIN_CURL_FAILED"

func globalCollectScript() string {
	return fmt.Sprintf(`
mark() { echo "===ORAMA_GLOBAL $1==="; }
chain_host=127.0.0.1
[ -e /etc/systemd/system/%s ] && chain_host=%s
# On a co-located machine the chain listens on the namespace address, which only root and the
# accounts the install allowed reach: ask through sudo, and say so when the answer does not come
# instead of printing nothing (a chain that is down and one that cannot be asked look the same).
chain_curl() {
  if [ "$chain_host" = %s ]; then sudo -n curl -sf --max-time 3 "$1"; else curl -sf --max-time 3 "$1"; fi || echo %s
}
unit_load() { systemctl show -p LoadState --value "$1" 2>/dev/null || echo unknown; }
unit_state() { systemctl is-active "$1" 2>/dev/null || true; }
mark chain_load
unit_load %s
mark chain_state
unit_state %s
mark status
chain_curl http://$chain_host:%d/status
mark net
chain_curl http://$chain_host:%d/net_info
mark validators
chain_curl "http://$chain_host:%d/validators?per_page=1"
mark params
chain_curl http://$chain_host:%d/cosmos/slashing/v1beta1/params
mark signing
chain_curl "http://$chain_host:%d/cosmos/slashing/v1beta1/signing_infos?pagination.limit=200"
mark staking
chain_curl "http://$chain_host:%d/cosmos/staking/v1beta1/validators?pagination.limit=200"
mark ipfs_load
unit_load %s
mark ipfs_state
unit_state %s
mark repo
if [ "$(systemctl is-active %s 2>/dev/null || true)" = active ]; then
  if sudo -n test -r %s/%s; then
    tok=$(sudo -n cat %s/%s | tr -d '\n')
    curl -sf --max-time 3 -X POST -H "Authorization: Bearer ${tok}" http://127.0.0.1:%d/api/v0/repo/stat || true
  else
    echo token-unreadable
  fi
fi
mark provider_load
unit_load %s
mark provider_state
unit_state %s
mark provider_monitor
sudo -n head -c 4096 %s/%s 2>/dev/null || true
mark relay_load
unit_load %s
mark relay_state
unit_state %s
mark relay_monitor
sudo -n head -c 4096 %s/%s 2>/dev/null || true
`,
		globalnetns.UnitName, constants.GlobalNetnsAddr, constants.GlobalNetnsAddr, chainCurlFailed,
		constants.ChainServiceUnit, constants.ChainServiceUnit,
		constants.ChainRPCPort, constants.ChainRPCPort, constants.ChainRPCPort,
		constants.ChainAPIPort, constants.ChainAPIPort, constants.ChainAPIPort,
		constants.GlobalIPFSUnit, constants.GlobalIPFSUnit, constants.GlobalIPFSUnit,
		constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile,
		constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile,
		constants.GlobalIPFSAPIPort,
		constants.GlobalProviderUnit, constants.GlobalProviderUnit,
		constants.GlobalProviderHome, constants.GlobalMonitorFile,
		constants.GlobalRelayUnit, constants.GlobalRelayUnit,
		constants.GlobalRelayHome, constants.GlobalMonitorFile,
	)
}

func splitGlobalSections(stdout string) map[string]string {
	const mark = "===ORAMA_GLOBAL "
	out := map[string]string{}
	parts := strings.Split(stdout, mark)
	for _, part := range parts[1:] {
		name, body, ok := strings.Cut(part, "===")
		if !ok {
			continue
		}
		out[strings.TrimSpace(name)] = strings.Trim(body, "\n")
	}
	return out
}

func parseGlobalCollect(stdout string, now time.Time) (*report.ChainReport, *report.GlobalReport) {
	sections := splitGlobalSections(stdout)
	return chainFromSections(sections, now), globalFromSections(sections)
}

func chainFromSections(sections map[string]string, now time.Time) *report.ChainReport {
	load := strings.TrimSpace(sections["chain_load"])
	if load == "" || load == "not-found" {
		return nil
	}
	state := strings.TrimSpace(sections["chain_state"])
	if state == "" {
		state = "inactive"
	}
	if state == "active" && strings.Contains(sections["status"], chainCurlFailed) {
		return &report.ChainReport{
			ServiceActive: true, UnitState: state,
			Error: "the chain unit is active but its RPC could not be read from this machine (co-located: sudo -n curl to the namespace address failed; check the node account's passwordless sudo)",
		}
	}
	for _, key := range []string{"status", "net", "validators", "params", "signing", "staking"} {
		if strings.Contains(sections[key], chainCurlFailed) {
			sections[key] = ""
		}
	}
	r := report.FillChainView([]byte(sections["status"]), []byte(sections["net"]), []byte(sections["validators"]), now)
	r.ServiceActive = state == "active"
	r.UnitState = state
	if !r.Responsive || r.ConsAddress == "" {
		return r
	}
	var signingPages, stakingPages [][]byte
	if strings.TrimSpace(sections["signing"]) != "" {
		signingPages = [][]byte{[]byte(sections["signing"])}
	}
	if strings.TrimSpace(sections["staking"]) != "" {
		stakingPages = [][]byte{[]byte(sections["staking"])}
	}
	view := report.InterpretSigning(r.ConsAddress, r.VotingPower, []byte(sections["params"]), signingPages, stakingPages)
	r.MissedBlockRatio = view.MissedBlockRatio
	r.MinSignedPerWindow = view.MinSignedPerWindow
	r.Jailed = view.Jailed
	r.Tombstoned = view.Tombstoned
	r.SigningError = view.Error
	return r
}

func globalFromSections(sections map[string]string) *report.GlobalReport {
	var g report.GlobalReport
	add := func(loadKey, stateKey, name string) string {
		load := strings.TrimSpace(sections[loadKey])
		if load == "" || load == "not-found" {
			return ""
		}
		state := strings.TrimSpace(sections[stateKey])
		if state == "" {
			state = "inactive"
		}
		g.Units = append(g.Units, report.GlobalUnit{Name: name, State: state})
		return state
	}
	ipfs := add("ipfs_load", "ipfs_state", constants.GlobalIPFSUnit)
	provider := add("provider_load", "provider_state", constants.GlobalProviderUnit)
	relay := add("relay_load", "relay_state", constants.GlobalRelayUnit)
	if ipfs == "" && provider == "" && relay == "" {
		return nil
	}
	if ipfs == "active" {
		g.PublicIPFS = publicIPFSFrom(sections["repo"])
	}
	if provider != "" {
		g.Provider = monitorProvider(sections["provider_monitor"])
	}
	if relay != "" {
		g.Relay = monitorRelay(sections["relay_monitor"])
	}
	return &g
}

func publicIPFSFrom(body string) *report.PublicIPFSReport {
	r := &report.PublicIPFSReport{}
	body = strings.TrimSpace(body)
	switch body {
	case "":
		r.Error = "public Kubo RPC did not answer"
	case "token-unreadable":
		r.Error = "public Kubo API token is not readable"
	default:
		repo, max, err := report.ParseRepoStat([]byte(body))
		if err != nil {
			r.Error = "public Kubo repo stat is not JSON"
			return r
		}
		r.RepoBytes = repo
		r.StorageMaxBytes = max
	}
	return r
}

func monitorProvider(body string) *report.ProviderReport {
	r := &report.ProviderReport{}
	if strings.TrimSpace(body) == "" {
		return r
	}
	mon, err := report.ParseMonitor([]byte(body))
	if err != nil {
		r.Error = "monitor file is not valid JSON"
		return r
	}
	r.HotKeyBalanceNorama = mon.HotKeyBalanceNorama
	r.ProofMisses = mon.ProofMisses
	r.DiskBytes = mon.DiskBytes
	r.StorageMaxBytes = mon.StorageMaxBytes
	return r
}

func monitorRelay(body string) *report.RelayReport {
	r := &report.RelayReport{}
	if strings.TrimSpace(body) == "" {
		return r
	}
	mon, err := report.ParseMonitor([]byte(body))
	if err != nil {
		r.Error = "monitor file is not valid JSON"
		return r
	}
	r.InConsensus = mon.InConsensus
	return r
}
