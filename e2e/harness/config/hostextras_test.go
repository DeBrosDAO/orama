package config

import "testing"

func TestStagenetHostListener_declaredAndUndeclared(t *testing.T) {
	cases := []struct {
		name    string
		node    string
		proto   string
		addr    string
		port    int
		process string
		want    bool
	}{
		{"tailscale udp on any port", "node-1", "udp", "0.0.0.0", 41641, "tailscaled", true},
		{"tailscale ephemeral tcp on the tailnet address", "node-3", "tcp", "100.101.102.103", 61927, "tailscaled", true},
		{"tailscale tcp on a wildcard is not excused", "node-3", "tcp", "0.0.0.0", 61927, "tailscaled", false},
		{"tailscale tcp on the public address is not excused", "node-3", "tcp", "203.0.113.9", 61927, "tailscaled", false},
		{"tailscale tcp just outside the CGNAT range", "node-3", "tcp", "100.128.0.1", 61927, "tailscaled", false},
		{"tailscale tcp on an unparseable address", "node-3", "tcp", "", 61927, "tailscaled", false},
		{"rpcbind on its port", "node-2", "tcp", "0.0.0.0", 111, "rpcbind", true},
		{"rpcbind on another port", "node-2", "tcp", "0.0.0.0", 112, "rpcbind", false},
		{"rpcbind on another node", "node-1", "tcp", "0.0.0.0", 111, "rpcbind", false},
		{"tailscale on a node without it", "node-2", "udp", "0.0.0.0", 41641, "tailscaled", false},
		{"an Orama process is never excused", "node-1", "tcp", "0.0.0.0", 10200, "app", false},
		{"empty process", "node-1", "tcp", "0.0.0.0", 41641, "", false},
	}
	for _, c := range cases {
		if _, got := StagenetHostListener(c.node, c.proto, c.addr, c.port, c.process); got != c.want {
			t.Errorf("%s: covered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStagenetHostUFWRule_declaredAndUndeclared(t *testing.T) {
	for _, node := range []string{"node-1", "node-2", "node-3"} {
		if _, ok := StagenetHostUFWRule(node, "Anywhere on tailscale0"); !ok {
			t.Errorf("%s: the tailscale rule is not declared", node)
		}
		if _, ok := StagenetHostUFWRule(node, "10200/tcp"); ok {
			t.Errorf("%s: an arbitrary port rule is declared", node)
		}
	}
	if got := StagenetHostUFWRulesOn("node-9"); len(got) != len(StagenetHostUFWRules) {
		t.Errorf("a node-wide declaration must apply to every node, got %v", got)
	}
}

func TestStagenetHostListenersOn_filtersByNode(t *testing.T) {
	if got := StagenetHostListenersOn("node-2"); len(got) != 2 {
		t.Errorf("node-2 declarations = %v, want the two rpcbind sockets", got)
	}
	if got := StagenetHostListenersOn("node-9"); len(got) != 0 {
		t.Errorf("an unknown node has no declarations, got %v", got)
	}
}

func TestStagenetHostListeners_everyDeclarationNamesAProcessAndWhy(t *testing.T) {
	for _, h := range StagenetHostListeners {
		if h.Process == "" || h.Proto == "" || h.Why == "" {
			t.Errorf("incomplete declaration %+v", h)
		}
	}
}
