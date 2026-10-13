//go:build e2e_fleet

package relayreporter

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// reportBlockers says why the reporter cannot report an epoch on this chain, in
// words for a skip, or return nothing when it can. They are the two things no
// reporter can overcome and the install does not make:
//   - its hot key is not in x/relay's reporter set (a reporter proposal adds it),
//     so x/relay refuses its report;
//   - the chain's epoch lasts less than one voting interval of the Tor network,
//     so an epoch holds no vote to judge uptime from and the reporter drops it
//     (reporter.ErrEpochTooShort).
//
// Without either the wait for a reported epoch can only run out.
func reportBlockers(address string, reporters []string, epoch, votingInterval time.Duration) []string {
	var out []string
	if !slices.Contains(reporters, address) {
		out = append(out, fmt.Sprintf("the reporter's address %s is not in x/relay's reporter set (%s): x/relay would refuse its report; add it with a reporter proposal and fund it", address, describeSet(reporters)))
	}
	if epoch < votingInterval {
		out = append(out, fmt.Sprintf("the chain's epochs last %s, shorter than the Tor network's voting interval of %s: an epoch holds no vote to judge uptime from, so the reporter drops every one", epoch, votingInterval))
	}
	return out
}

func describeSet(reporters []string) string {
	if len(reporters) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%d reporters: %s", len(reporters), strings.Join(reporters, ", "))
}

func TestReportBlockers_noneWhenTheKeyIsInTheSetAndEpochsAreLongEnough(t *testing.T) {
	const addr = "orama1reporter"
	if got := reportBlockers(addr, []string{"orama1other", addr}, time.Hour, 30*time.Minute); len(got) != 0 {
		t.Fatalf("blockers = %v", got)
	}
	if got := reportBlockers(addr, []string{addr}, 30*time.Minute, 30*time.Minute); len(got) != 0 {
		t.Fatalf("an epoch as long as the voting interval holds one vote: %v", got)
	}
}

func TestReportBlockers_keyAbsentFromTheSet(t *testing.T) {
	const addr = "orama1reporter"
	for name, set := range map[string][]string{"empty": nil, "others only": {"orama1a", "orama1b"}} {
		got := reportBlockers(addr, set, time.Hour, 30*time.Minute)
		if len(got) != 1 || !strings.Contains(got[0], addr) || !strings.Contains(got[0], "not in x/relay's reporter set") {
			t.Errorf("%s: blockers = %v", name, got)
		}
	}
	if got := reportBlockers(addr, nil, time.Hour, 30*time.Minute); !strings.Contains(got[0], "(empty)") {
		t.Errorf("an empty set is said to be empty: %v", got)
	}
}

func TestReportBlockers_epochShorterThanTheVotingInterval(t *testing.T) {
	const addr = "orama1reporter"
	got := reportBlockers(addr, []string{addr}, 5*time.Minute, 30*time.Minute)
	if len(got) != 1 || !strings.Contains(got[0], "5m0s") || !strings.Contains(got[0], "30m0s") {
		t.Fatalf("blockers = %v", got)
	}
}

func TestReportBlockers_bothAreSaid(t *testing.T) {
	if got := reportBlockers("orama1reporter", nil, 5*time.Minute, 30*time.Minute); len(got) != 2 {
		t.Fatalf("stagenet's state on its first night is both: %v", got)
	}
}
