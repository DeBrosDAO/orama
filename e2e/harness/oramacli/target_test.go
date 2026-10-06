package oramacli

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
)

// The stagenet target signs through the dev agent, whose socket is in the
// owner's home; the real ~/.rootwallet stays refused on every target.
func TestCheck_stagenetSocketInRealHome(t *testing.T) {
	home := "/nonexistent/real-home"
	cases := map[string]struct {
		target string
		sock   string
		ok     bool
	}{
		"fleet target refuses a socket in the home":    {config.TargetFleet, home + "/rwdev/agent.sock", false},
		"stagenet target accepts the dev agent":        {config.TargetStagenet, home + "/rwdev/agent.sock", true},
		"stagenet target refuses the real wallet":      {config.TargetStagenet, home + "/.rootwallet/agent.sock", false},
		"stagenet target refuses an empty socket":      {config.TargetStagenet, "", false},
		"stagenet target refuses a relative socket":    {config.TargetStagenet, "rwdev/agent.sock", false},
		"stagenet target refuses the wallet's subpath": {config.TargetStagenet, home + "/.rootwallet/x/agent.sock", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := newRunner(t)
			r.Target, r.AgentSock = c.target, c.sock
			_, err := r.Run(context.Background(), "version")
			if c.ok && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !c.ok && (err == nil || !strings.Contains(err.Error(), "refusing to run orama")) {
				t.Fatalf("err %v, want a refusal", err)
			}
		})
	}
}
