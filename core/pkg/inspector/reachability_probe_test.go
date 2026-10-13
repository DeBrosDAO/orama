package inspector

import (
	"strconv"
	"strings"
	"testing"
)

// A single lost packet over a WireGuard tunnel crossing the internet reported a
// healthy peer as unreachable, a critical failure: the probe must send more
// than one packet, each with a reply timeout, so only a host that answers none
// is unreachable.
func TestReachabilityProbe_oneLostPacketIsNotUnreachable(t *testing.T) {
	fields := strings.Fields(reachabilityProbe)
	if len(fields) == 0 || fields[0] != "ping" {
		t.Fatalf("probe %q is not a ping", reachabilityProbe)
	}
	flag := func(name string) string {
		for i, f := range fields {
			if f == name && i+1 < len(fields) {
				return fields[i+1]
			}
		}
		return ""
	}
	count, err := strconv.Atoi(flag("-c"))
	if err != nil || count < 3 {
		t.Errorf("probe %q sends %q packets, want at least 3", reachabilityProbe, flag("-c"))
	}
	if flag("-W") == "" {
		t.Errorf("probe %q sets no per-reply timeout", reachabilityProbe)
	}
	// An unprivileged ping refuses an interval below 0.2 s; the collector runs
	// as the SSH user.
	if interval, err := strconv.ParseFloat(flag("-i"), 64); err != nil || interval < 0.2 {
		t.Errorf("probe %q interval %q is below what an unprivileged ping accepts", reachabilityProbe, flag("-i"))
	}
}
