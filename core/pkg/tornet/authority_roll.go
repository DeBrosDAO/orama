package tornet

import (
	"fmt"
	"strings"
	"time"
)

// AuthorityLearnWindow is how long a directory authority that has just started
// casts no Running vote. Tor's TestingAuthDirTimeToLearnReachability defaults
// to 30 minutes ("After starting as an authority, do not make claims about
// whether routers are Running until this much time has passed"), and
// `AssumeReachable` does not shorten it. The option cannot be changed without
// TestingTorNetwork, which the network does not use.
//
// A consensus needs the Running flag in a majority of the votes, so with three
// authorities it takes the other two: an authority restarted while one of them
// is still inside its window leaves the round without the flag ("Nobody has
// voted on the Running flag ... Not generating a consensus!"), and a window
// that opens on all of them at once costs the network every consensus for
// about an hour, which expires the consensus every node holds.
const AuthorityLearnWindow = 30 * time.Minute

// RecentlyStartedAuthorities lists, one sentence each, the authorities of n
// other than self (a relay fingerprint) that cannot be counted on to vote
// Running at now, judged by the full consensus c: one that published its
// descriptor less than AuthorityLearnWindow ago has just started (an authority
// publishes a new descriptor when it starts), and one the consensus does not
// list at all is not up. An empty result means the other authorities have been
// voting for at least the window. The microdescriptor consensus carries no real
// publication times, so c must be the full one.
func RecentlyStartedAuthorities(n Network, c Consensus, self string, now time.Time) []string {
	var out []string
	for _, a := range n.Authorities {
		if strings.EqualFold(a.Fingerprint, self) {
			continue
		}
		r, ok := c.Listed(a.Fingerprint)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s is not listed in the consensus", a.Nickname))
		case now.Sub(r.Published) < AuthorityLearnWindow:
			out = append(out, fmt.Sprintf("%s published its descriptor %s ago, so it started less than %s ago and casts no Running vote yet",
				a.Nickname, now.Sub(r.Published).Round(time.Second), AuthorityLearnWindow))
		}
	}
	return out
}
