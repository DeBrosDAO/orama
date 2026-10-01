package inspector

import (
	"errors"
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
	ok := `{"results":[{"columns":["namespace_name","status"],"values":[["a","ready"],["b","provisioning"],["c"]]}]}`
	got := parseNamespaceRegistry(ok)
	if got["a"] != "ready" || got["b"] != "provisioning" || len(got) != 2 {
		t.Errorf("parsed %v", got)
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
	for status, want := range map[string]bool{
		"provisioning": true, "deprovisioning": true,
		"ready": false, "degraded": false, "failed": false, "": false,
	} {
		if got := (NamespaceData{RegistryStatus: status}).InTransition(); got != want {
			t.Errorf("%q: %v, want %v", status, got, want)
		}
	}
}
