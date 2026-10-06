//go:build e2e_fleet

package edge

import (
	"crypto/rand"
	"fmt"
	"net"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// ruleAbsent is `iptables -C`'s exit code when the rule does not exist.
const ruleAbsent = 1

// CutOff makes every TCP connection that user opens to dst:port on n fail at
// once (a reset, not a silent drop, so a client sees "refused" rather than
// waiting out its timeout). It isolates one process from one service without
// touching either: the service keeps serving everyone else. The rule carries
// a comment unique to the call; the cleanup, registered before the insert,
// deletes every copy and proves it is gone.
func CutOff(t testing.TB, f *fleet.Fleet, n fleet.Node, user, dst string, port int) {
	t.Helper()
	if net.ParseIP(dst) == nil {
		t.Fatalf("CutOff: %q is not an IP address", dst)
	}
	tag := make([]byte, 4)
	if _, err := rand.Read(tag); err != nil {
		t.Fatal(err)
	}
	comment := fmt.Sprintf("e2e-%s-%x", f.State.RunID, tag)
	spec := fmt.Sprintf("OUTPUT -p tcp -d %s --dport %d -m owner --uid-owner %s -m comment --comment %s -j REJECT --reject-with tcp-reset",
		dst, port, fleet.ShellQuote(user), fleet.ShellQuote(comment))
	check := "iptables -C " + spec + " 2>/dev/null"
	remove := fmt.Sprintf("while %s; do iptables -D %s || exit 1; done; %s; test $? -eq %d", check, spec, check, ruleAbsent)
	t.Cleanup(func() { RunInCleanup(t, f, n, remove) })
	f.MustExec(t, n, "iptables -I "+spec)
}
