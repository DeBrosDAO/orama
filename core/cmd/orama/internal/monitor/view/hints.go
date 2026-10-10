package view

import (
	"fmt"
	"net"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// commonProblemsDoc is where the runbooks for recurring failures live.
const commonProblemsDoc = "docs/COMMON_PROBLEMS.md"

// inspectSubsystems maps an alert subsystem to the `orama maint inspect
// --subsystem` value that checks it in depth.
var inspectSubsystems = map[string]string{
	"rqlite":    "rqlite",
	"olric":     "olric",
	"ipfs":      "ipfs",
	"dns":       "dns",
	"wireguard": "wg",
	"system":    "system",
	"network":   "network",
	"tor":       "tor",
}

// problemSections points a subsystem at the COMMON_PROBLEMS.md sections that
// cover it.
var problemSections = map[string]string{
	"rqlite":    "§6, §14, §15",
	"olric":     "§1, §7",
	"wireguard": "§1 (WireGuard packet loss)",
	"ipfs":      "§12",
	"namespace": "§1–§4",
}

// Hint is what to do next about an alert: a real command that looks closer,
// and the runbook section when there is one. Empty when there is nothing more
// specific to say than the alert itself.
func Hint(a cluster.Alert, env string) string {
	hint := hintCommand(a, env)
	if sec, ok := problemSections[a.Subsystem]; ok {
		doc := fmt.Sprintf("see %s %s", commonProblemsDoc, sec)
		if hint == "" {
			return doc
		}
		return hint + "; " + doc
	}
	return hint
}

func hintCommand(a cluster.Alert, env string) string {
	host := a.Node
	// A hint is a command to paste into a shell: only an address goes into
	// it. A node named by anything else (a self-reported hostname) gets the
	// cluster-wide form.
	if net.ParseIP(host) == nil {
		host = ""
	}
	if sub, ok := inspectSubsystems[a.Subsystem]; ok {
		return fmt.Sprintf("orama maint inspect --env %s --subsystem %s", env, sub)
	}
	switch a.Subsystem {
	case cluster.SubsystemCollection:
		if host == "" {
			return fmt.Sprintf("orama monitor --env %s --ssh", env)
		}
		return fmt.Sprintf("orama monitor node --env %s --node %s --ssh", env, host)
	case "service", "gateway", "namespace":
		if host == "" {
			return ""
		}
		return fmt.Sprintf("orama ssh %s --env %s 'sudo orama node status'", host, env)
	case "vault":
		if host == "" {
			return fmt.Sprintf("orama monitor node --env %s", env)
		}
		return fmt.Sprintf("orama monitor node --env %s --node %s", env, host)
	}
	return ""
}
