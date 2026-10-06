package config

import "testing"

const (
	testTailnetNet = "100.64.0.0/10"
	testWhy        = "test declaration"
)

// withDeclarations swaps the package's declarations for the duration of a test,
// so the matching rules are exercised whatever the real nodes declare.
func withDeclarations(t *testing.T, listeners []HostListener, rules []HostUFWRule) {
	t.Helper()
	oldL, oldR := StagenetHostListeners, StagenetHostUFWRules
	StagenetHostListeners, StagenetHostUFWRules = listeners, rules
	t.Cleanup(func() { StagenetHostListeners, StagenetHostUFWRules = oldL, oldR })
}

func TestStagenetHostListener_declaredAndUndeclared(t *testing.T) {
	withDeclarations(t, []HostListener{
		{Node: "node-1", Process: "tailscaled", Proto: "udp", Why: testWhy},
		{Node: "node-1", Process: "tailscaled", Proto: "tcp", Net: testTailnetNet, Why: testWhy},
		{Node: "node-2", Process: "rpcbind", Proto: "tcp", Port: 111, Why: testWhy},
		{Node: "node-2", Process: "rpcbind", Proto: "udp", Port: 111, Why: testWhy},
	}, nil)
	cases := []struct {
		name    string
		node    string
		proto   string
		addr    string
		port    int
		process string
		want    bool
	}{
		{"udp on any port", "node-1", "udp", "0.0.0.0", 41641, "tailscaled", true},
		{"tcp on the declared network", "node-1", "tcp", "100.101.102.103", 61927, "tailscaled", true},
		{"tcp on a wildcard is not excused by a network declaration", "node-1", "tcp", "0.0.0.0", 61927, "tailscaled", false},
		{"tcp on the public address is not excused", "node-1", "tcp", "203.0.113.9", 61927, "tailscaled", false},
		{"tcp just outside the declared range", "node-1", "tcp", "100.128.0.1", 61927, "tailscaled", false},
		{"tcp on an unparseable address", "node-1", "tcp", "", 61927, "tailscaled", false},
		{"declared port", "node-2", "tcp", "0.0.0.0", 111, "rpcbind", true},
		{"another port", "node-2", "tcp", "0.0.0.0", 112, "rpcbind", false},
		{"another node", "node-1", "tcp", "0.0.0.0", 111, "rpcbind", false},
		{"a node without the declaration", "node-2", "udp", "0.0.0.0", 41641, "tailscaled", false},
		{"an Orama process is never excused", "node-1", "tcp", "0.0.0.0", 10200, "app", false},
		{"empty process", "node-1", "tcp", "0.0.0.0", 41641, "", false},
	}
	for _, c := range cases {
		if _, got := StagenetHostListener(c.node, c.proto, c.addr, c.port, c.process); got != c.want {
			t.Errorf("%s: covered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStagenetHostListener_nodeWideDeclarationCoversEveryNode(t *testing.T) {
	withDeclarations(t, []HostListener{{Process: "agent", Proto: "tcp", Port: 7000, Why: testWhy}}, nil)
	for _, node := range []string{"node-1", "node-5"} {
		if _, ok := StagenetHostListener(node, "tcp6", "::", 7000, "agent"); !ok {
			t.Errorf("%s: a declaration without a node must cover it (tcp6 matches tcp)", node)
		}
	}
}

func TestStagenetHostUFWRule_declaredAndUndeclared(t *testing.T) {
	withDeclarations(t, nil, []HostUFWRule{{To: "Anywhere on tailscale0", Why: testWhy}})
	for _, node := range []string{"node-1", "node-2", "node-5"} {
		if _, ok := StagenetHostUFWRule(node, "Anywhere on tailscale0"); !ok {
			t.Errorf("%s: the declared rule is not found", node)
		}
		if _, ok := StagenetHostUFWRule(node, "10200/tcp"); ok {
			t.Errorf("%s: an arbitrary port rule is declared", node)
		}
	}
	if got := StagenetHostUFWRulesOn("node-9"); len(got) != 1 {
		t.Errorf("a node-wide declaration must apply to every node, got %v", got)
	}
}

func TestStagenetHostListenersOn_filtersByNode(t *testing.T) {
	withDeclarations(t, []HostListener{
		{Node: "node-2", Process: "rpcbind", Proto: "tcp", Port: 111, Why: testWhy},
		{Node: "node-2", Process: "rpcbind", Proto: "udp", Port: 111, Why: testWhy},
	}, nil)
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
	for _, r := range StagenetHostUFWRules {
		if r.To == "" || r.Why == "" {
			t.Errorf("incomplete ufw declaration %+v", r)
		}
	}
}

func TestStagenetHostExtras_declaredNodesAreStagenetNodes(t *testing.T) {
	known := map[string]bool{}
	for _, n := range StagenetNodes {
		known[n.Name] = true
	}
	for _, h := range StagenetHostListeners {
		if h.Node != "" && !known[h.Node] {
			t.Errorf("declaration %+v names a node that is not a stagenet node", h)
		}
	}
	for _, r := range StagenetHostUFWRules {
		if r.Node != "" && !known[r.Node] {
			t.Errorf("ufw declaration %+v names a node that is not a stagenet node", r)
		}
	}
}
