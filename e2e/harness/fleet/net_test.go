package fleet

import (
	"strings"
	"testing"
)

func countCmds(sh *fakeShell, part string) int {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	n := 0
	for _, c := range sh.cmds {
		if strings.Contains(c, part) {
			n++
		}
	}
	return n
}

func TestIPTablesBlock_insertsAndDeletes(t *testing.T) {
	sh := &fakeShell{}
	f := newFake(t, sh)
	t.Run("block", func(t *testing.T) {
		f.IPTablesBlock(t, f.Node(t, "node-1"), f.Node(t, "node-2"))
	})
	if inserts, deletes := countCmds(sh, "iptables -w -I "), countCmds(sh, "iptables -w -D "); inserts != 4 || deletes != 4 {
		t.Fatalf("inserts=%d deletes=%d: %v", inserts, deletes, sh.cmds)
	}
	if !strings.Contains(sh.cmds[0], "10.0.0.2") && !strings.Contains(sh.cmds[0], "203.0.113.2") {
		t.Fatalf("first rule targets the wrong node: %s", sh.cmds[0])
	}
	if !strings.Contains(sh.cmds[len(sh.cmds)-1], "test $? -eq 1") {
		t.Fatalf("the cleanup does not prove its rule is gone: %s", sh.cmds[len(sh.cmds)-1])
	}
	assertWaitsForLock(t, sh)
}

// assertWaitsForLock: every iptables invocation waits for the xtables lock.
func assertWaitsForLock(t *testing.T, sh *fakeShell) {
	t.Helper()
	for _, c := range sh.cmds {
		for _, tool := range []string{"iptables", "ip6tables"} {
			if strings.Count(c, tool+" ") != strings.Count(c, tool+" -w ") {
				t.Fatalf("%s runs without -w: %s", tool, c)
			}
		}
	}
}

// TestIPTablesBlock_twoTestsKeepEachOthersRule: two tests partitioning the
// same pair must hold distinct rules, so the first cleanup cannot lift the
// partition the second test still relies on.
func TestIPTablesBlock_twoTestsKeepEachOthersRule(t *testing.T) {
	sh := &fakeShell{}
	f := newFake(t, sh)
	var first, second []string
	t.Run("a", func(t *testing.T) {
		f.IPTablesBlock(t, f.Node(t, "node-1"), f.Node(t, "node-2"))
		first = inserted(sh)
		t.Run("b", func(t *testing.T) {
			f.IPTablesBlock(t, f.Node(t, "node-1"), f.Node(t, "node-2"))
			second = inserted(sh)[len(first):]
		})
	})
	if len(first) != 4 || len(second) != 4 {
		t.Fatalf("inserted %v then %v", first, second)
	}
	for i := range first {
		if first[i] == second[i] {
			t.Fatalf("both tests inserted the identical rule %q", first[i])
		}
	}
}

func inserted(sh *fakeShell) []string {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var out []string
	for _, c := range sh.cmds {
		if strings.HasPrefix(c, "iptables -w -I ") || strings.HasPrefix(c, "ip6tables -w -I ") {
			out = append(out, c)
		}
	}
	return out
}

// TestIPTablesBlock_cleanupRegisteredBeforeInsert: when the insert fails, the
// cleanup still runs (it may have been half applied).
func TestIPTablesBlock_cleanupRegisteredBeforeInsert(t *testing.T) {
	sh := &fakeShell{answer: func(cmd string) (Output, error) {
		if strings.Contains(cmd, " -I ") {
			return Output{Exit: 4, Stderr: "resource problem"}, nil
		}
		return Output{}, nil
	}}
	f := newFake(t, sh)
	if inner := runFailTB(t, func(tb testing.TB) {
		f.IPTablesBlock(tb, f.Node(t, "node-1"), f.Node(t, "node-2"))
	}); !inner.Failed() {
		t.Fatal("a failed insert was not reported")
	}
	if countCmds(sh, "iptables -w -D ") != 1 {
		t.Fatalf("no cleanup after a failed insert: %v", sh.cmds)
	}
}

func TestBlockRules_addressFamiliesAndValidation(t *testing.T) {
	rules, err := blockRules("r", "tag", Node{PublicIP: "1.2.3.4"})
	if err != nil || len(rules) != 2 || rules[0].tool != toolIPv4 {
		t.Fatalf("rules %v err %v", rules, err)
	}
	rules, err = blockRules("r", "tag", Node{PublicIP: "2001:db8::1", WGIP: "10.0.0.3"})
	if err != nil || len(rules) != 4 || rules[0].tool != toolIPv6 || rules[2].tool != toolIPv4 {
		t.Fatalf("rules %v err %v", rules, err)
	}
	if rules, err := blockRules("r", "tag", Node{}); err != nil || len(rules) != 0 {
		t.Fatalf("rules for a node with no address: %v %v", rules, err)
	}
	for _, bad := range []string{"1.2.3.4; reboot", "node-2", "10.0.0.1/24"} {
		if _, err := blockRules("r", "tag", Node{Name: "x", PublicIP: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
