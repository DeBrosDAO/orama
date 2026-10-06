package fleet

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// globalPublic is what a global node publishes, read from the constants the
// firewall's rules are built from (core/pkg/install/firewall.go
// globalPortSpecs): the chain's P2P over TCP and UDP, the public Kubo swarm
// over TCP and UDP, the storage provider, the Tor relay's ORPort and a
// dirauth's DirPort over TCP. Anything else tagged orama-global is not an edge
// port and is not excused.
var globalPublic = map[string]bool{
	proto("tcp", constants.ChainP2PPort):        true,
	proto("udp", constants.ChainP2PPort):        true,
	proto("tcp", constants.GlobalIPFSSwarmPort): true,
	proto("udp", constants.GlobalIPFSSwarmPort): true,
	proto("tcp", constants.GlobalProviderPort):  true,
	proto("tcp", constants.GlobalTorORPort):     true,
	proto("tcp", constants.GlobalTorDirPort):    true,
}

func proto(p string, port int) string { return p + "/" + strconv.Itoa(port) }

var portProto = regexp.MustCompile(`^(\d+)/(tcp|udp)$`)

// GlobalHostPorts reads `ufw status` and returns the global-node ports a
// listener on the HOST may serve (keyed "tcp/31000"): the ALLOW rules tagged
// orama-global that open the port on the host itself, which a global-only
// machine has ("31000/tcp ALLOW IN"). A forwarding rule ("198.18.0.2 31000/tcp
// ALLOW FWD") sends the port to the orama-global namespace, where the service
// listens; a host listener on that port is not what the rule is for, so it is
// not returned here.
func GlobalHostPorts(ufwStatus string) map[string]bool { return globalPorts(ufwStatus, false) }

// GlobalPublicPorts is every global-node port the node reaches the internet
// on: GlobalHostPorts plus the ports forwarded to the orama-global namespace.
// It answers "may this port be open from outside", not "may a host process
// listen on it".
func GlobalPublicPorts(ufwStatus string) map[string]bool { return globalPorts(ufwStatus, true) }

func globalPorts(ufwStatus string, forwarded bool) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(ufwStatus, "\n") {
		body, comment, ok := strings.Cut(line, "#")
		if !ok || strings.TrimSpace(comment) != "orama-global" || !strings.Contains(body, "ALLOW") {
			continue
		}
		if !forwarded && strings.Contains(body, "FWD") {
			continue
		}
		for _, f := range strings.Fields(body) {
			m := portProto.FindStringSubmatch(f)
			if m == nil {
				continue
			}
			if p, _ := strconv.Atoi(m[1]); globalPublic[proto(m[2], p)] {
				out[proto(m[2], p)] = true
			}
		}
	}
	return out
}
