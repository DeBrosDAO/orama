package install

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// The gateway drives a deployment's unit as the unprivileged orama user. Every
// verb it uses has to pass the privileged helper, or a tenant's deploy fails on
// a node with a permissions error rather than anything about the deployment.
func TestPrivHelper_allowsEveryVerbTheGatewayUsesOnADeployment(t *testing.T) {
	unit := process.UnitName(process.Runtime("node"), "acme", "web.v2")
	for _, verb := range process.SystemctlVerbs {
		argv := []string{privhelper.ToolSystemctl, verb, unit}
		if verb == "set-property" {
			argv = append(argv, "MemoryMax=512M", "CPUQuota=150%")
		}
		if _, err := privhelper.Validate(argv); err != nil {
			t.Errorf("the helper refuses %q, which the gateway calls on every deployment: %v", argv, err)
		}
	}
}

// Regression for the bug where the orama user could not run `ufw`, so
// AddWebRTCRules silently failed and TURN relay ports stayed firewalled after
// `webrtc enable`: every rule the node adds or deletes must pass the helper.
func TestPrivHelper_allowsEveryTURNFirewallRule(t *testing.T) {
	for _, add := range webRTCRuleArgs(defaultTURNRelayPortStart, defaultTURNRelayPortEnd) {
		for _, args := range [][]string{add, append([]string{"delete"}, add...)} {
			if _, err := privhelper.Validate(append([]string{privhelper.ToolUFW}, args...)); err != nil {
				t.Errorf("the helper refuses ufw %v: %v", args, err)
			}
		}
	}
}
