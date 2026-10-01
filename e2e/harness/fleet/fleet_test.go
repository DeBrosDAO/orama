package fleet

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// fakeShell answers commands from a script and records what it was asked.
// It keeps files with their modes and answers `test -e`, `stat -c %a` and
// `rm -f` on them itself; everything else goes to answer.
type fakeShell struct {
	mu      sync.Mutex
	cmds    []string
	files   map[string][]byte
	modes   map[string]os.FileMode
	answer  func(cmd string) (Output, error)
	putErrs error
	// probeExit, when set, is what `test -e` exits with whatever the files.
	probeExit int
}

func (s *fakeShell) Run(_ context.Context, cmd string) (Output, error) {
	s.mu.Lock()
	s.cmds = append(s.cmds, cmd)
	out, handled := s.fileCmd(cmd)
	s.mu.Unlock()
	if handled {
		return out, nil
	}
	if s.answer == nil {
		return Output{}, nil
	}
	return s.answer(cmd)
}

// fileCmd answers the file commands the helpers run; s.mu is held.
func (s *fakeShell) fileCmd(cmd string) (Output, bool) {
	if p, ok := strings.CutPrefix(cmd, "test -e "); ok {
		if s.probeExit != 0 {
			return Output{Exit: s.probeExit}, true
		}
		if _, exists := s.files[p]; exists {
			return Output{}, true
		}
		return Output{Exit: 1}, true
	}
	if p, ok := strings.CutPrefix(cmd, "stat -c %a "); ok {
		if _, exists := s.files[p]; !exists {
			return Output{Exit: 1, Stderr: "No such file"}, true
		}
		return Output{Stdout: strconv.FormatUint(uint64(s.modes[p]), 8) + "\n"}, true
	}
	if p, ok := strings.CutPrefix(cmd, "rm -f "); ok {
		delete(s.files, p)
		return Output{}, true
	}
	return Output{}, false
}

func (s *fakeShell) Put(_ context.Context, path string, data []byte, mode os.FileMode) error {
	if s.putErrs != nil {
		return s.putErrs
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = append([]byte{}, data...)
	s.modes[path] = mode
	s.cmds = append(s.cmds, "PUT "+path)
	return nil
}

func (s *fakeShell) Get(_ context.Context, path string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.files[path]
	if !ok {
		return nil, errors.New("no such file")
	}
	return d, nil
}

func testState() *State {
	return &State{RunID: "ab12", Nodes: []Node{
		{Name: "node-1", PublicIP: "203.0.113.1", WGIP: "10.0.0.1"},
		{Name: "node-2", PublicIP: "203.0.113.2", WGIP: "10.0.0.2"},
	}, Extras: []Node{{Name: "extra-1", PublicIP: "203.0.113.9"}}, Probes: []Node{{Name: "probe-1", PublicIP: "198.51.100.1"}}}
}

func newFake(t *testing.T, sh *fakeShell) *Fleet {
	t.Helper()
	if sh.files == nil {
		sh.files = map[string][]byte{}
	}
	if sh.modes == nil {
		sh.modes = map[string]os.FileMode{}
	}
	rec, err := evidence.New(t.TempDir(), "fleet", nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewWithDialer(testState(), rec, func(State, Node) Shell { return sh })
}

func TestLookup_byNameAndAddresses(t *testing.T) {
	f := newFake(t, &fakeShell{})
	for _, key := range []string{"node-2", "203.0.113.2", "10.0.0.2"} {
		if n, ok := f.Lookup(key); !ok || n.Name != "node-2" {
			t.Errorf("%s: got %+v %v", key, n, ok)
		}
	}
	if n, ok := f.Lookup("probe-1"); !ok || n.PublicIP != "198.51.100.1" {
		t.Errorf("probe lookup: %+v", n)
	}
	if _, ok := f.Lookup(""); ok {
		t.Error("empty key matched a node with no WG ip")
	}
	if len(f.AllNodes()) != 3 {
		t.Errorf("AllNodes %v", f.AllNodes())
	}
}

func TestExec_recordsEvidence(t *testing.T) {
	sh := &fakeShell{answer: func(string) (Output, error) { return Output{Stdout: "ok", Stderr: "warn", Exit: 3}, nil }}
	f := newFake(t, sh)
	out := f.Exec(t, f.Node(t, "node-1"), "true")
	if out.Exit != 3 || out.Stdout != "ok" {
		t.Fatalf("out %+v", out)
	}
}

func TestStopService_cleanupStartsAndWaits(t *testing.T) {
	calls := 0
	sh := &fakeShell{answer: func(cmd string) (Output, error) {
		if strings.HasPrefix(cmd, "systemctl is-active") {
			calls++
			if calls == 2 {
				return Output{Stdout: "inactive\n", Exit: 3}, nil
			}
			return Output{Stdout: "active\n"}, nil
		}
		return Output{}, nil
	}}
	f := newFake(t, sh)
	t.Run("stop", func(t *testing.T) {
		f.StopService(t, f.Node(t, "node-1"), "orama-node.service")
	})
	// One read of the prior state, then the cleanup polled twice.
	if calls != 3 {
		t.Fatalf("is-active ran %d times", calls)
	}
	if countCmds(sh, "systemctl reset-failed orama-node.service") == 0 {
		t.Fatalf("the cleanup never reset the failed state: %v", sh.cmds)
	}
}

// TestKill_restoresAnInactiveUnitToInactive: a unit that was not running
// before the test is stopped again by the cleanup, not started.
func TestKill_restoresAnInactiveUnitToInactive(t *testing.T) {
	sh := &fakeShell{answer: func(cmd string) (Output, error) {
		if strings.HasPrefix(cmd, "systemctl is-active orama") {
			return Output{Stdout: "inactive\n", Exit: 3}, nil
		}
		return Output{}, nil
	}}
	f := newFake(t, sh)
	t.Run("kill", func(t *testing.T) {
		f.Kill(t, f.Node(t, "node-1"), "orama-node.service")
	})
	if countCmds(sh, "systemctl start") != 0 || countCmds(sh, "systemctl stop orama-node.service") != 1 {
		t.Fatalf("an inactive unit was not left inactive: %v", sh.cmds)
	}
}

func TestShellQuote_escapes(t *testing.T) {
	if got := ShellQuote(`a'b`); got != `'a'"'"'b'` {
		t.Fatalf("got %s", got)
	}
}

func TestParseSS_lines(t *testing.T) {
	out := `tcp   LISTEN 0      4096       10.0.0.1:6001      0.0.0.0:*    users:(("orama-node",pid=12,fd=3))
tcp   LISTEN 0      128           [::]:22            [::]:*    users:(("sshd",pid=1,fd=4))
udp   UNCONN 0      0       127.0.0.53%lo:53         0.0.0.0:*    users:(("systemd-resolve",pid=9,fd=13))
udp   UNCONN 0      0          0.0.0.0:51820      0.0.0.0:*
`
	ls, err := ParseSS(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 4 || ls[0].Port != 6001 || ls[0].Process != "orama-node" || ls[0].Public() {
		t.Fatalf("ls %+v", ls)
	}
	if ls[1].Addr != "::" || !ls[1].Public() || ls[2].Addr != "127.0.0.53" || ls[3].Process != "" || !ls[3].Public() {
		t.Fatalf("ls %+v", ls)
	}
	for _, bad := range []string{"tcp LISTEN", "tcp LISTEN 0 1 nocolon *:*", "tcp LISTEN 0 1 1.2.3.4:x *:*"} {
		if _, err := ParseSS(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if ls, err := ParseSS(""); err != nil || len(ls) != 0 {
		t.Fatalf("empty: %v %v", ls, err)
	}
}

// ss writes a scoped IPv6 address with the zone after the bracket; the address
// comes out bare, as an unscoped one does.
func TestParseSS_scopedLinkLocalAddress(t *testing.T) {
	ls, err := ParseSS(`udp   UNCONN 0      0      [fe80::f816:3eff:fe59:ce1e]%ens3:546          [::]:*    users:(("systemd-network",pid=7,fd=21))` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Addr != "fe80::f816:3eff:fe59:ce1e" || ls[0].Port != 546 || ls[0].Public() {
		t.Fatalf("ls %+v", ls)
	}
}

func TestParseUFW_statusAndAllows(t *testing.T) {
	out := `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere
51820/udp                  ALLOW IN    Anywhere
6001/tcp                   ALLOW       10.0.0.0/24
22/tcp (v6)                ALLOW       Anywhere (v6)
`
	fw := ParseUFW(out)
	if !fw.Active || len(fw.Rules) != 4 {
		t.Fatalf("fw %+v", fw)
	}
	if !fw.Allows("22/tcp") || !fw.Allows("51820/udp") || fw.Allows("6001/tcp") || fw.Allows("443/tcp") {
		t.Fatalf("allows wrong: %+v", fw.Rules)
	}
	if ParseUFW("Status: inactive\n").Active {
		t.Fatal("inactive parsed as active")
	}
}
