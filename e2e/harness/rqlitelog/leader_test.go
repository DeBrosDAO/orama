package rqlitelog

import "testing"

func TestParseLeaderLine(t *testing.T) {
	for name, c := range map[string]struct {
		line string
		at   float64
		host string
		ok   bool
	}{
		"journal line": {"1790987548.695829 vps-2c5fe48a orama-rqlite-x[306430]: [store] 2026/10/03 00:32:28 node 10.0.0.5:10001 at 10.0.0.5:10001 is now Leader",
			1790987548.695829, "10.0.0.5", true},
		"id with spaces": {"17 h u: node some id at 10.0.0.2:10001 is now Leader", 17, "10.0.0.2", true},
		"ipv6":           {"17 h u: node x at [fd00::1]:10001 is now Leader", 17, "fd00::1", true},
		"leader unknown": {"17 h u: [store] Leader is now unknown", 0, "", false},
		"bad time":       {"yesterday h u: node x at 10.0.0.2:10001 is now Leader", 0, "", false},
		"no port":        {"17 h u: node x at 10.0.0.2 is now Leader", 0, "", false},
		"empty":          {"", 0, "", false},
	} {
		at, host, ok := ParseLeaderLine(c.line)
		if ok != c.ok || at != c.at || host != c.host {
			t.Errorf("%s: ParseLeaderLine = %v, %q, %t; want %v, %q, %t", name, at, host, ok, c.at, c.host, c.ok)
		}
	}
}
