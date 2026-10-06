//go:build e2e_fleet

package infra

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Firewall rule tags (core/pkg/install/firewall.go ownedRuleComment and
// GlobalRuleComment): Reconcile owns only rules tagged orama; orama-global
// rules belong to the global node and a cluster reconcile never touches them.
const (
	TagOrama  = "orama"
	TagGlobal = "orama-global"
	// OverlayRuleTo is how `ufw status` prints the mesh rule's destination
	// ("ufw allow in on wg0 from 10.0.0.0/24").
	OverlayRuleTo = "Anywhere on " + WireGuardIface
)

// UFWRule is one row of `ufw status`, with its comment.
type UFWRule struct {
	To, Action, From, Comment string
	V6                        bool
}

var ufwCols = regexp.MustCompile(`\s{2,}`)

// ParseUFWRules reads the rule rows of `ufw status`, keeping the comment
// fleet.ParseUFW drops.
func ParseUFWRules(out string) []UFWRule {
	var rules []UFWRule
	inRules := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "--"):
			inRules = true
		case inRules && trimmed != "":
			body, comment, _ := strings.Cut(trimmed, "#")
			cols := ufwCols.Split(strings.TrimSpace(body), -1)
			if len(cols) < 3 {
				continue
			}
			r := UFWRule{To: cols[0], Action: cols[1], From: cols[2], Comment: strings.TrimSpace(comment)}
			r.V6 = strings.Contains(r.To, "(v6)") || strings.Contains(r.From, "(v6)")
			r.To = strings.TrimSpace(strings.ReplaceAll(r.To, "(v6)", ""))
			rules = append(rules, r)
		}
	}
	return rules
}

// UFWRules reads n's live rules.
func UFWRules(t testing.TB, f *fleet.Fleet, n fleet.Node) []UFWRule {
	t.Helper()
	return ParseUFWRules(f.MustExec(t, n, "ufw status").Stdout)
}

// HasRule reports whether an ALLOW rule for to exists with the given tag.
func HasRule(rules []UFWRule, to, tag string) bool {
	for _, r := range rules {
		if r.To == to && strings.HasPrefix(r.Action, "ALLOW") && r.Comment == tag {
			return true
		}
	}
	return false
}

// TURNRules are the rules a node that relays TURN keeps (and that
// `webrtc enable` adds at runtime, tagged orama, until the next reconcile).
var TURNRules = []string{"3478/udp", "3478/tcp", "5349/tcp", "49152:65535/udp"}

// TURNConfigPath is the shared TURN server's config: it exists exactly while
// the node holds a TURN allocation (core/pkg/constants/paths.go
// HostTURNConfigPath), which is what Reconcile decides the TURN rules by.
const TURNConfigPath = OramaDir + "/data/turn/turn.yaml"

// DesiredPublicRules is the allow set Reconcile converges a node to
// (core/pkg/install/firewall.go GenerateRules): SSH, WireGuard, HTTP(S), DNS
// on a nameserver, TURN when the host relays, and the mesh on wg0.
func DesiredPublicRules(n fleet.Node, turn bool) map[string]bool {
	want := map[string]bool{"22/tcp": true, "51820/udp": true, "80/tcp": true, "443/tcp": true, OverlayRuleTo: true}
	if n.Role == fleet.RoleNameserver {
		want["53/tcp"], want["53/udp"] = true, true
	}
	if turn {
		for _, r := range TURNRules {
			want[r] = true
		}
	}
	return want
}

// HostRunsTURN reports whether n holds a TURN allocation, the file test
// Reconcile itself uses (core/pkg/install/orchestrator.go hostRunsTURN).
func HostRunsTURN(t testing.TB, f *fleet.Fleet, n fleet.Node) bool {
	t.Helper()
	return f.Exec(t, n, "test -e "+TURNConfigPath).Exit == 0
}
