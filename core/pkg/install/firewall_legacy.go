package install

import (
	"fmt"
	"strconv"
	"strings"
)

// Rules are tagged `comment orama` so Reconcile can tell its own from an
// operator's. Releases before the tag added rules without it, and some of
// those rules are no longer wanted: Reconcile leaves untagged rules alone, so
// on an upgraded node they stayed open for good. legacyAllowRules is that set,
// read out of the git history of the firewall code, and Reconcile deletes a
// live rule only when it matches an entry exactly — the same rule AND the same
// comment, which for most of them is none. An operator who wants one of these
// ports open adds the rule with a comment of their own, and it no longer
// matches.

// legacyAllowRule is a rule an older Orama added, as `ufw status` shows it.
type legacyAllowRule struct {
	rule    string
	comment string
}

// Pre-tag TURN allocated each namespace an 800-port relay block from
// 49152-65535, aligned to the start of the range (pkg/namespace
// findAvailablePortBlock), and opened it on its own; the root firewall now
// opens the whole range, tagged, on nodes that relay.
const (
	legacyTURNRelayRangeStart = 49152
	legacyTURNRelayRangeEnd   = 65535
	legacyTURNRelayBlockSize  = 800
)

// legacyAllowRules are the exact rules to remove:
//
//   - 2025 setup code (before 0388c3a), with its comments: the Anyone relay's
//     ORPort and ControlPort, and Olric, IPFS and IPFS Cluster ports that
//     have since moved into the 10100 block on loopback or the overlay.
//   - GenerateRules before rules were tagged: the Anyone relay ORPort (removed
//     in e684f9f), TURN TLS on 443/udp (714a986), the /8 overlay allow
//     narrowed to 10.0.0.0/24 (fd87eec), and that /24 allow itself, which
//     admitted the overlay's addresses on every interface. The tagged copy of
//     the /24 goes as any other owned rule nobody wants: the overlay is now
//     admitted on wg0 only (overlayAllowRule).
//   - The per-namespace TURN relay blocks (legacyTURNRelayBlocks).
//
// Never SSH: legacyRulesToRemove refuses any rule on the configured SSH port,
// whatever this list says.
var legacyAllowRules = append([]legacyAllowRule{
	{"9001/tcp", "Anon ORPort"},
	{"9051/tcp", "Anon ControlPort"},
	{"3320/tcp", "Olric HTTP API"},
	{"3322/tcp", "Olric Memberlist"},
	{"4001/tcp", "IPFS Swarm"},
	{"5001/tcp", "IPFS API"},
	{"9094/tcp", "IPFS Cluster API"},
	{"9096/tcp", "IPFS Cluster Swarm"},
	{"9001/tcp", ""},
	{"443/udp", ""},
	{"from 10.0.0.0/8", ""},
	{"from 10.0.0.0/24", ""},
}, legacyTURNRelayBlocks()...)

// legacyTURNRelayBlocks are the untagged per-namespace relay rules.
func legacyTURNRelayBlocks() []legacyAllowRule {
	var rules []legacyAllowRule
	for start := legacyTURNRelayRangeStart; start+legacyTURNRelayBlockSize-1 <= legacyTURNRelayRangeEnd; start += legacyTURNRelayBlockSize {
		rules = append(rules, legacyAllowRule{rule: fmt.Sprintf("%d:%d/udp", start, start+legacyTURNRelayBlockSize-1)})
	}
	return rules
}

// legacyRulesToRemove are the live rows that exactly match a legacy rule.
// A rule on sshPort, or one the node still wants, is never returned.
func legacyRulesToRemove(rows []allowRow, sshPort int, wanted map[string]bool) []string {
	legacy := make(map[legacyAllowRule]bool, len(legacyAllowRules))
	for _, r := range legacyAllowRules {
		legacy[r] = true
	}
	var remove []string
	for _, row := range rows {
		if !legacy[legacyAllowRule{rule: row.rule, comment: row.comment}] {
			continue
		}
		if wanted[row.rule] || rulePort(row.rule) == strconv.Itoa(sshPort) {
			continue
		}
		remove = append(remove, row.rule)
	}
	return remove
}

// rulePort is the port of a "<port>/<proto>" rule, or "" for any other shape.
func rulePort(rule string) string {
	port, _, ok := strings.Cut(rule, "/")
	if !ok || strings.Contains(port, " ") {
		return ""
	}
	return port
}
