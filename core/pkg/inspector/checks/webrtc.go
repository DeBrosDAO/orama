package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("webrtc", CheckWebRTC)
}

const webrtcSub = "webrtc"

// CheckWebRTC runs WebRTC (SFU/TURN) health checks.
// These checks only apply to namespaces that have SFU or TURN provisioned.
func CheckWebRTC(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		results = append(results, checkWebRTCPerNode(nd)...)
	}

	results = append(results, checkWebRTCCrossNode(data)...)

	return results
}

func checkWebRTCPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	node := nd.Node.Name()

	for _, ns := range nd.Namespaces {
		// Only check SFU/TURN if they are provisioned on this node.
		// A false value when not provisioned is not an error.
		hasSFU := ns.SFUUp   // true = service active
		hasTURN := ns.TURNUp // true = service active

		// If neither is provisioned, skip WebRTC checks for this namespace
		if !hasSFU && !hasTURN {
			continue
		}

		prefix := fmt.Sprintf("ns.%s", ns.Name)

		if hasSFU {
			r = append(r, inspector.Pass(prefix+".sfu_up",
				fmt.Sprintf("Namespace %s SFU active", ns.Name),
				webrtcSub, node, "systemd service running", inspector.High))
		}

		if hasTURN {
			r = append(r, inspector.Pass(prefix+".turn_up",
				fmt.Sprintf("Namespace %s TURN active", ns.Name),
				webrtcSub, node, "systemd service running", inspector.High))
		}
	}

	return r
}

func checkWebRTCCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	// Collect SFU/TURN node counts per namespace
	type webrtcCounts struct {
		sfuNodes  int
		turnNodes int
	}
	nsCounts := map[string]*webrtcCounts{}

	for _, nd := range data.Nodes {
		for _, ns := range nd.Namespaces {
			if !ns.SFUUp && !ns.TURNUp {
				continue
			}
			c, ok := nsCounts[ns.Name]
			if !ok {
				c = &webrtcCounts{}
				nsCounts[ns.Name] = c
			}
			if ns.SFUUp {
				c.sfuNodes++
			}
			if ns.TURNUp {
				c.turnNodes++
			}
		}
	}

	for name, counts := range nsCounts {
		// SFU should be on all cluster nodes (typically 3)
		if counts.sfuNodes > 0 {
			if counts.sfuNodes >= 3 {
				r = append(r, inspector.Pass(
					fmt.Sprintf("ns.%s.sfu_coverage", name),
					fmt.Sprintf("Namespace %s SFU on all nodes", name),
					webrtcSub, "",
					fmt.Sprintf("%d SFU nodes active", counts.sfuNodes),
					inspector.High))
			} else {
				r = append(r, inspector.Warn(
					fmt.Sprintf("ns.%s.sfu_coverage", name),
					fmt.Sprintf("Namespace %s SFU on all nodes", name),
					webrtcSub, "",
					fmt.Sprintf("only %d/3 SFU nodes active", counts.sfuNodes),
					inspector.High))
			}
		}

		// TURN should be on 2 nodes
		if counts.turnNodes > 0 {
			if counts.turnNodes >= 2 {
				r = append(r, inspector.Pass(
					fmt.Sprintf("ns.%s.turn_coverage", name),
					fmt.Sprintf("Namespace %s TURN redundant", name),
					webrtcSub, "",
					fmt.Sprintf("%d TURN nodes active", counts.turnNodes),
					inspector.High))
			} else {
				r = append(r, inspector.Warn(
					fmt.Sprintf("ns.%s.turn_coverage", name),
					fmt.Sprintf("Namespace %s TURN redundant", name),
					webrtcSub, "",
					fmt.Sprintf("only %d/2 TURN nodes active (no redundancy)", counts.turnNodes),
					inspector.High))
			}
		}
	}

	return r
}
