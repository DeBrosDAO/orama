package fleet

import "testing"

const colocatedUFW = `Status: active

     To                         Action      From
     --                         ------      ----
22/tcp                     ALLOW IN    Anywhere                   # orama
Anywhere                   ALLOW FWD   198.18.0.2 on ogl-host     # orama-global
198.18.0.2 31000/tcp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31000/udp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31010/tcp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31010/udp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31013/tcp       ALLOW FWD   Anywhere                   # orama-global
`

func TestGlobalPublicPorts_colocatedForwardRules(t *testing.T) {
	got := GlobalPublicPorts(colocatedUFW)
	for _, want := range []string{"tcp/31000", "udp/31000", "tcp/31010", "udp/31010", "tcp/31013"} {
		if !got[want] {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	if len(got) != 5 {
		t.Errorf("got %v, want exactly the five global ports", got)
	}
}

func TestGlobalPublicPorts_globalOnlyDirectRules(t *testing.T) {
	got := GlobalPublicPorts("31000/tcp                   ALLOW IN    Anywhere                   # orama-global\n")
	if len(got) != 1 || !got["tcp/31000"] {
		t.Errorf("got %v, want tcp/31000 only", got)
	}
}

func TestGlobalPublicPorts_clusterNodeHasNone(t *testing.T) {
	if got := GlobalPublicPorts("22/tcp ALLOW IN Anywhere # orama\n80/tcp ALLOW IN Anywhere # orama\n"); len(got) != 0 {
		t.Errorf("a cluster node has no global ports, got %v", got)
	}
	if got := GlobalPublicPorts(""); len(got) != 0 {
		t.Errorf("empty status has none, got %v", got)
	}
}

func TestGlobalPublicPorts_neverExcusesWhatIsNotGlobalOrNotTagged(t *testing.T) {
	status := "31000/tcp ALLOW IN Anywhere\n" + // untagged
		"31050/tcp ALLOW IN Anywhere # orama-global\n" + // not a published global port
		"31000/tcp DENY IN Anywhere # orama-global\n" + // not an allow
		"10200/tcp ALLOW IN Anywhere # orama-global\n" // outside the block
	if got := GlobalPublicPorts(status); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}
