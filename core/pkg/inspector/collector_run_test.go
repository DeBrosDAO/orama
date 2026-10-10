package inspector

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The collectors of a node run side by side (bounded), and a failure is filed
// under its own subsystem.
func TestRunCollectors_runsConcurrentlyBoundedAndFilesFailures(t *testing.T) {
	var running, peak atomic.Int32
	job := func(subsystem string, err error) collectorJob {
		return collectorJob{subsystem, func() error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
			return err
		}}
	}
	var jobs []collectorJob
	for i := 0; i < 10; i++ {
		jobs = append(jobs, job("ok", nil))
	}
	jobs = append(jobs, job(SubsystemTor, errors.New("tor down")))

	nd := &NodeData{}
	runCollectors(nd, jobs)

	if got := peak.Load(); got < 2 || got > maxConcurrentCollectors {
		t.Errorf("peak concurrency %d, want between 2 and %d", got, maxConcurrentCollectors)
	}
	if len(nd.Failed) != 1 || nd.Failed[SubsystemTor] != "tor down" {
		t.Errorf("failures = %v, want only tor", nd.Failed)
	}
}

func TestRunCollectors_noJobs(t *testing.T) {
	nd := &NodeData{}
	runCollectors(nd, nil)
	if nd.Failed != nil || len(nd.Errors) != 0 {
		t.Errorf("no jobs recorded %v %v", nd.Failed, nd.Errors)
	}
}

// Sessions to one node share a connection: with the 1-4s a session costs on a
// starved node, fourteen separate ones took longer than the whole --timeout.
func TestSharedConnectionOptions(t *testing.T) {
	if got := (Node{}).sharedConnectionOptions(); got != nil {
		t.Errorf("a node with no ControlDir got %v", got)
	}
	got := strings.Join(Node{ControlDir: "/tmp/orama-inspect-1"}.sharedConnectionOptions(), " ")
	for _, want := range []string{"ControlMaster=auto", "ControlPath=/tmp/orama-inspect-1/%C", "ControlPersist="} {
		if !strings.Contains(got, want) {
			t.Errorf("options %q lack %q", got, want)
		}
	}
}

func TestParseNamespaceRegistry(t *testing.T) {
	ok := `{"results":[{"values":[["a","ready",86400],["b","provisioning",0],["c","deprovisioning",null],["d"]]}]}`
	got := parseNamespaceRegistry(ok)
	if len(got) != 3 || got["a"].status != "ready" || got["a"].age != 86400*time.Second {
		t.Errorf("parsed %v", got)
	}
	if got["b"].age != 0 || got["b"].status != "provisioning" {
		t.Errorf("a namespace stamped this second must read age 0, known: %v", got["b"])
	}
	if got["c"].age != UnknownTransitionAge {
		t.Errorf("a missing stamp must read unknown: %v", got["c"])
	}
	for name, raw := range map[string]string{
		"empty":       "",
		"unreachable": `{"error":"unreachable"}`,
		"sql error":   `{"results":[{"error":"no such table"}]}`,
		"not json":    "curl: (7) failed",
		"no rows":     `{"results":[{"columns":["x"]}]}`,
	} {
		if got := parseNamespaceRegistry(raw); len(got) != 0 {
			t.Errorf("%s: parsed %v, want none (judged as settled)", name, got)
		}
	}
}

func TestNamespaceInTransition(t *testing.T) {
	cases := []struct {
		status    string
		age       time.Duration
		in, stuck bool
	}{
		{"provisioning", 0, true, false},
		{"provisioning", time.Minute, true, false},
		{"deprovisioning", namespaceTransitionLimit - time.Second, true, false},
		{"deprovisioning", namespaceTransitionLimit, false, true},
		{"provisioning", UnknownTransitionAge, false, false},
		{"ready", time.Minute, false, false},
		{"degraded", time.Hour, false, false},
		{"failed", time.Hour, false, false},
		{"", 0, false, false},
	}
	for _, c := range cases {
		n := NamespaceData{RegistryStatus: c.status, TransitionAge: c.age}
		if n.InTransition() != c.in || n.StuckInTransition() != c.stuck {
			t.Errorf("%q age %v: in=%v stuck=%v, want in=%v stuck=%v", c.status, c.age, n.InTransition(), n.StuckInTransition(), c.in, c.stuck)
		}
	}
}

// A collector that panics is that subsystem's failure; the others still finish
// and the process is not ended.
func TestRunCollectors_panicIsAFailure(t *testing.T) {
	var ran atomic.Int32
	nd := &NodeData{}
	runCollectors(nd, []collectorJob{
		{SubsystemRQLite, func() error { panic("unexpected output") }},
		{SubsystemOlric, func() error { ran.Add(1); return nil }},
	})
	if ran.Load() != 1 {
		t.Error("a panic in one collector stopped another")
	}
	if got := nd.Failed[SubsystemRQLite]; !strings.Contains(got, "panicked") || !strings.Contains(got, "unexpected output") {
		t.Errorf("rqlite failure = %q, want the panic named", got)
	}
}

// Failures arrive in the order the jobs finish; the report lists them in one
// order whatever it was.
func TestRunCollectors_errorsAreSorted(t *testing.T) {
	for i := 0; i < 20; i++ {
		nd := &NodeData{}
		var jobs []collectorJob
		for _, sub := range []string{SubsystemTor, SubsystemDNS, SubsystemIPFS, SubsystemOlric, SubsystemRQLite} {
			jobs = append(jobs, collectorJob{sub, func() error { return errors.New("down") }})
		}
		runCollectors(nd, jobs)
		if !slices.IsSorted(nd.Errors) || len(nd.Errors) != 5 {
			t.Fatalf("errors not sorted: %v", nd.Errors)
		}
	}
}
