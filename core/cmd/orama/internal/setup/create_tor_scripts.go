package setup

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// The commands setup runs on a machine for its Tor role. Like scripts.go, they are
// built here with every value quoted, so the tests read exactly what reaches the machine.

const (
	markTorNetwork = "__TOR_NETWORK__"
	markTorNow     = "__TOR_NOW__"
	markTorUnit    = "__TOR_UNIT__"
	markTorActive  = "__TOR_ACTIVE__"
	markTorInfo    = "__TOR_INFO__"
	markTorPending = "__TOR_PENDING__"
)

// torServiceOf is the install name of a Tor role.
func torServiceOf(role TorRole) install.GlobalService {
	if role == TorDirauth {
		return install.GlobalServiceDirauth
	}
	return install.GlobalServiceRelay
}

// torUnitOf and torHomeOf are the unit and the DataDirectory of a Tor role.
func torUnitOf(role TorRole) string {
	if role == TorDirauth {
		return constants.GlobalTorDirauthUnit
	}
	return constants.GlobalTorRelayUnit
}

func torHomeOf(role TorRole) string {
	if role == TorDirauth {
		return constants.GlobalTorDirauthHome
	}
	return constants.GlobalTorRelayHome
}

// authorityKeysCommand is `orama maint global tor authority-keys` for an authority.
func authorityKeysCommand(in AuthorityKeysInput) string {
	q := clusterops.ShellQuote
	return strings.Join([]string{"sudo", orama, "maint", "global", "tor", "authority-keys",
		"--nickname", q(in.Nickname), "--address", q(in.Address)}, " ")
}

// ParseAuthorityKeys reads what authorityKeysCommand printed: the document is the
// last line, whatever a package install printed before it.
func ParseAuthorityKeys(out string) (tornet.AuthorityKeys, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return tornet.ParseAuthorityKeys([]byte(strings.TrimSpace(lines[len(lines)-1])))
}

// TorInstallCommand is `orama global install` for one Tor role of a machine that
// already runs the chain: only the Tor service is named, so the chain layer is not
// rewritten. The network file is staged beside the release by the caller.
func TorInstallCommand(in TorInstall) string {
	q := clusterops.ShellQuote
	services := string(torServiceOf(in.Role))
	if in.Role == TorRelay && in.Exit {
		services += ",exit"
	}
	parts := []string{"sudo", orama, "global", "install", "--colocated",
		"--services", q(services),
		"--staged-dir", stagedBinDir, "--manifest", install.DefaultStagedManifest,
		"--tor-address", q(in.IP), "--tor-contact", q(in.Contact)}
	if in.Role == TorDirauth {
		parts = append(parts, "--tor-authority-keys", q(in.Bundle))
	} else {
		parts = append(parts, "--tor-node-id", q(in.NodeID))
	}
	return strings.Join(parts, " ")
}

// torLifecycleCommand is `orama global <verb> <role>`.
func torLifecycleCommand(verb string, role TorRole) string {
	return strings.Join([]string{"sudo", orama, "global", verb, string(torServiceOf(role))}, " ")
}

// torStateScript prints what the machine has of a Tor role: the digest of the
// installed network file, the machine's clock, whether the role's unit is installed
// and active, and what `orama global tor info` says. A role that is not installed has
// no info; the command's failure is an empty section, never an error.
func torStateScript(role TorRole) string {
	return strings.NewReplacer("{NET}", markTorNetwork, "{NOW}", markTorNow, "{UNITMARK}", markTorUnit, "{ACTIVEMARK}", markTorActive, "{PENDINGMARK}", markTorPending, "{INFOMARK}", markTorInfo,
		"{NETFILE}", constants.GlobalStateRoot+"/"+constants.GlobalTorAuthoritiesFile, "{UNITFILE}", unitDir+"/"+torUnitOf(role), "{UNIT}", torUnitOf(role),
		"{TORRC}", constants.GlobalTorrcFor(torHomeOf(role)), "{CLI}", orama).Replace(`echo {NET}
sha256sum {NETFILE} 2>/dev/null | cut -d' ' -f1 || true
echo {NOW}
date -u +%s
echo {UNITMARK}
if [ -f {UNITFILE} ]; then echo 1; else echo 0; fi
echo {ACTIVEMARK}
systemctl is-active {UNIT} || true
echo {PENDINGMARK}
ts=$(TZ=UTC systemctl show {UNIT} -p ActiveEnterTimestamp --value 2>/dev/null); if [ -n "$ts" ]; then started=$(date -d "$ts" +%s 2>/dev/null || echo 0); else started=0; fi
rc=$(stat -c %Y {TORRC} 2>/dev/null || echo 0)
if [ "$rc" -gt "$started" ]; then echo 1; else echo 0; fi
echo {INFOMARK}
{CLI} global tor info --json 2>/dev/null || true
`)
}

// ParseTorState reads torStateScript's output for the role.
func ParseTorState(out string, role TorRole) (TorState, error) {
	sec := splitMarked(out, markTorNetwork, markTorNow, markTorUnit, markTorActive, markTorPending, markTorInfo)
	var st TorState
	if sum := strings.TrimSpace(sec[markTorNetwork]); sum != "" {
		if !sha256Hex.MatchString(sum) {
			return TorState{}, fmt.Errorf("the installed Tor network file's digest %q is not a SHA-256", sum)
		}
		st.NetworkSHA256 = sum
	}
	now, err := strconv.ParseInt(strings.TrimSpace(sec[markTorNow]), 10, 64)
	if err != nil {
		return TorState{}, fmt.Errorf("the machine's clock %q is not a Unix time", strings.TrimSpace(sec[markTorNow]))
	}
	st.Now = time.Unix(now, 0).UTC()
	switch u := strings.TrimSpace(sec[markTorUnit]); u {
	case "0", "1":
		st.Installed = u == "1"
	default:
		return TorState{}, fmt.Errorf("the unit check printed %q, not 0 or 1", u)
	}
	st.Active = strings.TrimSpace(sec[markTorActive]) == "active"
	switch p := strings.TrimSpace(sec[markTorPending]); p {
	case "0", "1":
		st.RestartPending = p == "1"
	default:
		return TorState{}, fmt.Errorf("the restart check printed %q, not 0 or 1", p)
	}
	info, err := parseTorInfo(sec[markTorInfo], role)
	if err != nil {
		return TorState{}, err
	}
	st.Info = info
	return st, nil
}

// parseTorInfo picks the role's entry from the JSON array `orama global tor info --json`
// prints; nothing printed, or no entry for the role, is no info.
func parseTorInfo(raw string, role TorRole) (*tornet.NodeInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var infos []tornet.NodeInfo
	if err := json.Unmarshal([]byte(raw), &infos); err != nil {
		return nil, fmt.Errorf("`orama global tor info --json` printed something that is not a list of roles: %w (%.80q)", err, raw)
	}
	for i := range infos {
		if infos[i].Home == torHomeOf(role) {
			if infos[i].Error != "" {
				return nil, fmt.Errorf("the %s's tor data directory cannot be read: %s", role, oneLine(infos[i].Error))
			}
			return &infos[i], nil
		}
	}
	return nil, nil
}
