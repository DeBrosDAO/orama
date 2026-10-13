package view

import (
	"fmt"
	"net"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// commonProblemsDoc is where the runbooks for recurring failures live.
const commonProblemsDoc = "orama.network/docs/operator/troubleshooting"

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

// problemSections points a subsystem at the anchors of the troubleshooting page
// sections that cover it.
var problemSections = map[string][]string{
	"rqlite":    {"raft-quorum-issues", "rqlite-replication-lag"},
	"olric":     {"olric-cluster-problems", "olric-cluster-split-after-enabling-encryption"},
	"wireguard": {"wireguard-connectivity"},
	"ipfs":      {"ipfs-cluster-pins-never-replicate"},
	"namespace": {"namespace-gateway-olric-unavailable", "namespace-gateway-missing-config-fields", "namespace-not-restoring-after-a-restart-missing-cluster-statejson", "namespace-services-not-restarting-after-an-upgrade"},
}

// Hint is what to do next about an alert: a real command that looks closer,
// and the runbook section when there is one. Empty when there is nothing more
// specific to say than the alert itself.
func Hint(a cluster.Alert, env string) string {
	hint := hintCommand(a, env)
	if anchors, ok := problemSections[a.Subsystem]; ok {
		links := make([]string, len(anchors))
		for i, anchor := range anchors {
			links[i] = commonProblemsDoc + "#" + anchor
		}
		doc := "see " + strings.Join(links, ", ")
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
			return fmt.Sprintf("orama status --env %s --ssh", env)
		}
		return fmt.Sprintf("orama status node --env %s --node %s --ssh", env, host)
	case "service", "gateway", "namespace":
		if host == "" {
			return ""
		}
		return fmt.Sprintf("orama ssh %s --env %s 'sudo orama node status'", host, env)
	case "vault":
		if host == "" {
			return fmt.Sprintf("orama status node --env %s", env)
		}
		return fmt.Sprintf("orama status node --env %s --node %s", env, host)
	}
	return ""
}
