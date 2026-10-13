package report

import (
	"io/fs"
	"testing"
)

// cgroups maps a pid to its /proc/<pid>/cgroup; a pid it lacks has exited.
func cgroupsOf(m map[int]string) func(int) (string, error) {
	return func(pid int) (string, error) {
		c, ok := m[pid]
		if !ok {
			return "", fs.ErrNotExist
		}
		return c, nil
	}
}

// The ps output of a co-located stagenet node: every daemon is a child of init
// because systemd started it, and the global services were counted as orphans
// because they were not in a hand-kept list of units.
const colocatedPS = `
  100     1 S orama-node
  101     1 S orama-cosmovisor
  102     1 S orama-global
  103     1 S orama
  104     1 S rqlited
  105     1 S ipfs
  106     1 S tor
  107   104 S rqlited
`

func TestClassifyProcesses_serviceUnitsOwnTheirProcesses(t *testing.T) {
	cg := cgroupsOf(map[int]string{
		100: "0::/system.slice/orama-node.service\n",
		101: "0::/system.slice/orama-global-chain.service\n",
		102: "0::/system.slice/orama-global-indexer.service\n",
		103: "0::/system.slice/orama-global-txgate.service\n",
		104: "0::/system.slice/system-orama\\x2dnamespace\\x2drqlite.slice/orama-namespace-rqlite@rootwallet.service\n",
		105: "0::/system.slice/orama-global-ipfs.service\n",
		106: "0::/system.slice/system-orama\\x2dnamespace\\x2dtor.slice/orama-namespace-tor@index.service\n",
	})
	zombies, orphans := classifyProcesses(colocatedPS, cg)
	if len(zombies) != 0 || len(orphans) != 0 {
		t.Fatalf("zombies=%v orphans=%v, want none: every process is in a service unit", zombies, orphans)
	}
}

func TestClassifyProcesses_strayProcessesAreOrphans(t *testing.T) {
	cg := cgroupsOf(map[int]string{
		100: "0::/user.slice/user-1000.slice/session-12.scope\n",                      // nohup'd from an ssh login
		101: "0::/system.slice/run-r1a2b.scope\n",                                     // a scope, not a service
		102: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/x.service\n", // a user manager's unit
		103: "0::/\n",
		104: "0::/system.slice/orama-node.service\n",
	})
	_, orphans := classifyProcesses(colocatedPS, cg)
	var got []int
	for _, o := range orphans {
		got = append(got, o.PID)
	}
	want := []int{100, 101, 102, 103}
	if len(got) != len(want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orphans = %v, want %v", got, want)
		}
	}
}

func TestClassifyProcesses_exitedProcessIsNotCounted(t *testing.T) {
	_, orphans := classifyProcesses("  100     1 S orama\n", cgroupsOf(nil))
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none: the process exited after ps ran", orphans)
	}
}

func TestClassifyProcesses_unreadableCgroupProvesNoOwner(t *testing.T) {
	_, orphans := classifyProcesses("  100     1 S orama\n", func(int) (string, error) {
		return "", fs.ErrPermission
	})
	if len(orphans) != 1 {
		t.Fatalf("orphans = %v, want the process: nothing shows a unit owns it", orphans)
	}
}

func TestClassifyProcesses_onlyOramaChildrenOfInitAreCandidates(t *testing.T) {
	ps := "  100     1 S sshd\n  101   555 S orama\n  102     1 Z orama\n"
	cg := cgroupsOf(map[int]string{100: "0::/user.slice\n", 101: "0::/user.slice\n", 102: "0::/user.slice\n"})
	zombies, orphans := classifyProcesses(ps, cg)
	if len(zombies) != 1 || zombies[0].PID != 102 {
		t.Errorf("zombies = %v, want pid 102", zombies)
	}
	if len(orphans) != 1 || orphans[0].PID != 102 {
		t.Errorf("orphans = %v, want only pid 102 (parent init, orama, no unit)", orphans)
	}
}

func TestClassifyProcesses_emptyAndMalformedOutput(t *testing.T) {
	z, o := classifyProcesses("\n  \nnot a ps line\n", cgroupsOf(nil))
	if len(z) != 0 || len(o) != 0 {
		t.Fatalf("zombies=%v orphans=%v from malformed output", z, o)
	}
}

func TestOwningServiceUnit(t *testing.T) {
	cases := []struct{ name, cgroup, want string }{
		{"v2 service", "0::/system.slice/orama-node.service\n", "orama-node.service"},
		{"v2 nested slice", "0::/system.slice/system-orama\\x2dnamespace\\x2dgateway.slice/orama-namespace-gateway@index.service\n", "orama-namespace-gateway@index.service"},
		{"v1 systemd hierarchy", "12:cpu,cpuacct:/user.slice\n1:name=systemd:/system.slice/caddy.service\n", "caddy.service"},
		{"v1 other hierarchies only", "12:cpu,cpuacct:/system.slice/caddy.service\n", ""},
		{"session scope", "0::/user.slice/user-1000.slice/session-3.scope\n", ""},
		{"root", "0::/\n", ""},
		{"empty", "", ""},
		{"garbage", "nonsense\n", ""},
	}
	for _, c := range cases {
		if got := owningServiceUnit(c.cgroup); got != c.want {
			t.Errorf("%s: owningServiceUnit = %q, want %q", c.name, got, c.want)
		}
	}
}
