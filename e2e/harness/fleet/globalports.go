package fleet

import (
	"regexp"
	"strconv"
	"strings"
)

// globalPublic is what a global node publishes
// (core/pkg/install/firewall.go GlobalAllowArgs, core/pkg/constants/global.go):
// the chain's P2P over TCP and UDP, the public Kubo swarm over TCP and UDP,
// the storage provider over TCP. Anything else tagged orama-global is not an
// edge port and is not excused.
var globalPublic = map[string]bool{
	"tcp/31000": true, "udp/31000": true,
	"tcp/31010": true, "udp/31010": true,
	"tcp/31013": true,
}

var portProto = regexp.MustCompile(`^(\d+)/(tcp|udp)$`)

// GlobalPublicPorts reads `ufw status` and returns the global-node ports the
// node opened (keyed "tcp/31000"): the allow rules tagged orama-global whose
// port is one a global node publishes. A machine that shares a cluster node
// forwards them to the orama-global namespace ("198.18.0.2 31000/tcp ALLOW
// FWD"), a global-only machine allows them directly ("31000/tcp ALLOW IN");
// both name the port as a port/proto field. A cluster node has none.
func GlobalPublicPorts(ufwStatus string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(ufwStatus, "\n") {
		body, comment, ok := strings.Cut(line, "#")
		if !ok || strings.TrimSpace(comment) != "orama-global" || !strings.Contains(body, "ALLOW") {
			continue
		}
		for _, f := range strings.Fields(body) {
			m := portProto.FindStringSubmatch(f)
			if m == nil {
				continue
			}
			if p, _ := strconv.Atoi(m[1]); globalPublic[m[2]+"/"+strconv.Itoa(p)] {
				out[m[2]+"/"+strconv.Itoa(p)] = true
			}
		}
	}
	return out
}
