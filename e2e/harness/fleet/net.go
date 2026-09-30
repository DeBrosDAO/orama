package fleet

import (
	"crypto/rand"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Listener is one listening socket from `ss -H -ltnup`.
type Listener struct {
	Proto   string // tcp or udp
	Addr    string // bind address without brackets or %iface: 0.0.0.0, ::, 10.0.0.1, *
	Port    int
	Process string // first process name, empty when ss shows none
}

var ssProcess = regexp.MustCompile(`\(\("([^"]+)"`)

// ParseSS parses `ss -H -ltnup` output.
func ParseSS(out string) ([]Listener, error) {
	var ls []Listener
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("ss line %d has %d fields: %q", i+1, len(fields), line)
		}
		local := fields[4]
		sep := strings.LastIndex(local, ":")
		if sep < 0 {
			return nil, fmt.Errorf("ss line %d: local address %q has no port", i+1, local)
		}
		port, err := strconv.Atoi(local[sep+1:])
		if err != nil {
			return nil, fmt.Errorf("ss line %d: port in %q: %w", i+1, local, err)
		}
		addr := strings.Trim(local[:sep], "[]")
		if pct := strings.Index(addr, "%"); pct >= 0 {
			addr = addr[:pct]
		}
		l := Listener{Proto: fields[0], Addr: addr, Port: port}
		if m := ssProcess.FindStringSubmatch(line); m != nil {
			l.Process = m[1]
		}
		ls = append(ls, l)
	}
	return ls, nil
}

// Public reports whether the listener accepts connections on every interface.
func (l Listener) Public() bool {
	return l.Addr == "0.0.0.0" || l.Addr == "::" || l.Addr == "*"
}

// Listeners returns node's listening sockets.
func (f *Fleet) Listeners(t testing.TB, n Node) []Listener {
	t.Helper()
	out := f.MustExec(t, n, "ss -H -ltnup")
	ls, err := ParseSS(out.Stdout)
	if err != nil {
		t.Fatalf("failed to parse ss on %s: %v", n.Name, err)
	}
	return ls
}

// FirewallRule is one row of `ufw status`.
type FirewallRule struct {
	To     string // "22/tcp", "51820/udp", "22/tcp (v6)"
	Action string // ALLOW, DENY, REJECT, LIMIT (with IN/OUT when given)
	From   string // "Anywhere", "10.0.0.0/24"
}

// Firewall is a node's ufw state.
type Firewall struct {
	Active bool
	Rules  []FirewallRule
}

var ufwColumns = regexp.MustCompile(`\s{2,}`)

// ParseUFW parses `ufw status` output.
func ParseUFW(out string) Firewall {
	var fw Firewall
	inRules := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Status:"):
			fw.Active = strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:")) == "active"
		case strings.HasPrefix(trimmed, "--"):
			inRules = true
		case inRules && trimmed != "":
			cols := ufwColumns.Split(trimmed, -1)
			if len(cols) >= 3 {
				fw.Rules = append(fw.Rules, FirewallRule{To: cols[0], Action: cols[1], From: cols[2]})
			}
		}
	}
	return fw
}

// Allows reports whether an ALLOW rule opens portProto ("443/tcp") to From
// "Anywhere" on IPv4.
func (fw Firewall) Allows(portProto string) bool {
	for _, r := range fw.Rules {
		if r.To == portProto && strings.HasPrefix(r.Action, "ALLOW") && r.From == "Anywhere" {
			return true
		}
	}
	return false
}

// Firewall returns node's ufw state.
func (f *Fleet) Firewall(t testing.TB, n Node) Firewall {
	t.Helper()
	return ParseUFW(f.MustExec(t, n, "ufw status").Stdout)
}

// IPTablesBlock partitions from and to: on from, it drops everything to and
// from to's public and WireGuard addresses (iptables for IPv4, ip6tables for
// IPv6). SSH from the runner is untouched.
//
// Each call's rules carry a comment unique to the call, so two tests blocking
// the same pair hold two rules and each cleanup deletes only its own: the
// partition lasts until the last test that asked for it ends. The cleanup is
// registered before a rule is inserted and loops until its rule is gone, so
// a failed or interrupted insert still leaves nothing behind.
func (f *Fleet) IPTablesBlock(t testing.TB, from, to Node) {
	t.Helper()
	rules, err := blockRules(f.State.RunID, rand.Text()[:blockTagLength], to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatalf("node %s has no address to block", to.Name)
	}
	for _, r := range rules {
		t.Cleanup(func() { f.cleanupExec(t, from, r.removeAll()) })
		f.MustExec(t, from, r.tool+" -I "+r.spec)
	}
}

// blockTagLength is how many random base32 characters make a rule's comment unique.
const blockTagLength = 8

// Firewall tools per address family. -w waits for the xtables lock: without
// it a rule change racing another (a parallel test, the node's own ufw or
// orama) fails with exit 4 instead of applying.
const (
	toolIPv4 = "iptables -w"
	toolIPv6 = "ip6tables -w"
)

// ruleAbsent is the exit code of `iptables -C` when the rule does not exist
// (2 and above are errors that say nothing about the rule).
const ruleAbsent = 1

// blockRule is one rule spec (without -I/-D/-C) and the tool that owns it.
type blockRule struct {
	tool string
	spec string
}

// removeAll deletes every copy of the rule, then proves it is gone.
func (r blockRule) removeAll() string {
	check := r.tool + " -C " + r.spec + " 2>/dev/null"
	return fmt.Sprintf("while %s; do %s -D %s || exit 1; done; %s; test $? -eq %d", check, r.tool, r.spec, check, ruleAbsent)
}

// blockRules are the rules that isolate to, tagged with the run and tag. An
// address that is set must parse as an IP: it is put in a root shell command.
func blockRules(runID, tag string, to Node) ([]blockRule, error) {
	comment := "-m comment --comment " + ShellQuote("e2e-"+runID+"-"+tag)
	var out []blockRule
	for _, addr := range []string{to.PublicIP, to.WGIP} {
		if addr == "" {
			continue
		}
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("node %s: %q is not an IP address", to.Name, addr)
		}
		tool := toolIPv6
		if ip.To4() != nil {
			tool = toolIPv4
		}
		out = append(out,
			blockRule{tool: tool, spec: fmt.Sprintf("OUTPUT -d %s %s -j DROP", ip, comment)},
			blockRule{tool: tool, spec: fmt.Sprintf("INPUT -s %s %s -j DROP", ip, comment)})
	}
	return out, nil
}
