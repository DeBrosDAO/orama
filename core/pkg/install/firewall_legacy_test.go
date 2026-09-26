package install

import (
	"strings"
	"testing"
)

// A node upgraded from every era: pre-tag rules alongside the tagged set and
// the operator's own.
const legacyNodeStatus = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere                   # orama
51820/udp                  ALLOW       Anywhere                   # orama
443/tcp                    ALLOW       Anywhere                   # orama
Anywhere                   ALLOW       10.0.0.0/24                # orama
9001/tcp                   ALLOW       Anywhere                   # Anon ORPort
4001/tcp                   ALLOW       Anywhere                   # IPFS Swarm
5001/tcp                   ALLOW       Anywhere                   # IPFS API
443/udp                    ALLOW       Anywhere
Anywhere                   ALLOW       10.0.0.0/8
49152:49951/udp            ALLOW       Anywhere
50752:51551/udp            ALLOW       Anywhere
Anywhere on tailscale0     ALLOW       Anywhere
9100/tcp                   ALLOW       Anywhere
3478/udp                   ALLOW       Anywhere
9001/tcp (v6)              ALLOW       Anywhere (v6)              # Anon ORPort
`

func TestLegacyRulesToRemove_removesTheExactHistoricalRules(t *testing.T) {
	got := legacyRulesToRemove(parseAllowRows(legacyNodeStatus), 22, map[string]bool{})
	want := []string{"9001/tcp", "4001/tcp", "5001/tcp", "443/udp", "from 10.0.0.0/8", "49152:49951/udp", "50752:51551/udp"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The operator's rules, the runtime TURN listeners orama-node opens, interface
// rules and SSH are never touched, even untagged.
func TestLegacyRulesToRemove_leavesEverythingElse(t *testing.T) {
	got := legacyRulesToRemove(parseAllowRows(legacyNodeStatus), 22, map[string]bool{})
	for _, never := range []string{"22/tcp", "9100/tcp", "3478/udp", "443/tcp", "51820/udp", "from 10.0.0.0/24"} {
		for _, r := range got {
			if r == never {
				t.Errorf("%s is not a legacy rule and must stay", never)
			}
		}
	}
}

// Exact means the comment too: the operator reopening 4001 with a comment of
// their own, or a pre-tag rule text with a different comment, is not ours.
func TestLegacyRulesToRemove_requiresTheExactComment(t *testing.T) {
	status := `4001/tcp                   ALLOW       Anywhere                   # my ipfs
9001/tcp                   ALLOW       Anywhere                   # tor relay
443/udp                    ALLOW       Anywhere                   # quic
49152:49951/udp            ALLOW       Anywhere                   # my relay
`
	if got := legacyRulesToRemove(parseAllowRows(status), 22, map[string]bool{}); len(got) != 0 {
		t.Fatalf("removed %v; every row carries an operator's comment", got)
	}
}

// An SSH daemon on a port a legacy rule also names keeps its rule: losing it
// locks the operator out of the node.
func TestLegacyRulesToRemove_neverTheSSHPort(t *testing.T) {
	status := "9001/tcp                   ALLOW       Anywhere\n"
	if got := legacyRulesToRemove(parseAllowRows(status), 9001, map[string]bool{}); len(got) != 0 {
		t.Fatalf("removed %v, the SSH port", got)
	}
}

// A rule the node wants is not removed however it is written.
func TestLegacyRulesToRemove_neverAWantedRule(t *testing.T) {
	status := "443/udp                    ALLOW       Anywhere\n"
	if got := legacyRulesToRemove(parseAllowRows(status), 22, map[string]bool{"443/udp": true}); len(got) != 0 {
		t.Fatalf("removed %v, which the node wants", got)
	}
}

func TestLegacyRulesToRemove_emptyStatus(t *testing.T) {
	if got := legacyRulesToRemove(parseAllowRows("Status: inactive\n"), 22, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// The list itself: never SSH, never a rule the current firewall generates, and
// the relay blocks are the allocator's aligned 800-port blocks inside the range.
func TestLegacyAllowRules_shape(t *testing.T) {
	desired := map[string]bool{}
	for _, cfg := range []FirewallConfig{{}, {IsNameserver: true, TURNEnabled: true, TURNRelayStart: defaultTURNRelayPortStart, TURNRelayEnd: defaultTURNRelayPortEnd}} {
		for _, r := range NewFirewallProvisioner(cfg).DesiredAllowRules() {
			desired[r] = true
		}
	}
	for _, r := range legacyAllowRules {
		if rulePort(r.rule) == "22" {
			t.Errorf("legacy list names SSH: %v", r)
		}
		if desired[r.rule] {
			t.Errorf("legacy rule %q is one the firewall still generates", r.rule)
		}
	}

	blocks := legacyTURNRelayBlocks()
	if len(blocks) != 20 {
		t.Fatalf("got %d relay blocks, want 20 (800-port blocks in 49152-65535)", len(blocks))
	}
	if blocks[0].rule != "49152:49951/udp" || blocks[19].rule != "64352:65151/udp" {
		t.Errorf("relay blocks run %s .. %s", blocks[0].rule, blocks[19].rule)
	}
}

func TestParseAllowRows_readsTheComment(t *testing.T) {
	rows := parseAllowRows(legacyNodeStatus)
	found := false
	for _, r := range rows {
		if r.rule == "9001/tcp" {
			found = true
			if r.comment != "Anon ORPort" {
				t.Errorf("comment = %q", r.comment)
			}
		}
		if strings.Contains(r.rule, "(v6)") {
			t.Errorf("v6 row parsed: %q", r.rule)
		}
	}
	if !found {
		t.Fatal("9001/tcp row not parsed")
	}
}

// An untagged overlay allow from before rules were tagged admitted the mesh's
// addresses on every interface; the tagged one Reconcile added since is an
// owned rule nobody wants any more and goes the ordinary way.
func TestLegacyRulesToRemove_removesTheUntaggedOverlayAllow(t *testing.T) {
	status := `Anywhere                   ALLOW       10.0.0.0/24
Anywhere on wg0            ALLOW       10.0.0.0/24                # orama
`
	wanted := map[string]bool{}
	for _, r := range NewFirewallProvisioner(FirewallConfig{}).DesiredAllowRules() {
		wanted[r] = true
	}
	got := legacyRulesToRemove(parseAllowRows(status), 22, wanted)
	if strings.Join(got, "|") != "from 10.0.0.0/24" {
		t.Fatalf("got %v, want the untagged /24 allow removed", got)
	}
}
