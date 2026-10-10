package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
)

// fakeDNS is an in-memory zone that behaves like cloudflare.Client.
type fakeDNS struct {
	mu      sync.Mutex
	records []cloudflare.Record
	next    int
}

func (f *fakeDNS) RunSubdomain(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", fmt.Errorf("bad run id %q", runID)
	}
	return namePrefix + runID + "." + testZone, nil
}

func (f *fakeDNS) RunIDOf(name string) (string, bool) {
	rest, ok := strings.CutSuffix(name, "."+testZone)
	if !ok {
		return "", false
	}
	labels := strings.Split(rest, ".")
	owned, ok := strings.CutPrefix(labels[len(labels)-1], namePrefix)
	id, _, _ := strings.Cut(owned, "-")
	return id, ok
}

func (f *fakeDNS) add(typ, name, content string, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.records = append(f.records, cloudflare.Record{ID: fmt.Sprint(f.next), Type: typ, Name: name, Content: content, CreatedOn: created})
}

func (f *fakeDNS) Delegate(_ context.Context, sub string, nss []cloudflare.Nameserver) error {
	for _, ns := range nss {
		if !f.has("NS", sub, ns.Hostname+"."+sub) {
			f.add("NS", sub, ns.Hostname+"."+sub, time.Now())
		}
		if !f.has("A", ns.Hostname+"."+sub, ns.IP) {
			f.add("A", ns.Hostname+"."+sub, ns.IP, time.Now())
		}
	}
	return nil
}

func (f *fakeDNS) has(typ, name, content string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.records {
		if r.Type == typ && r.Name == name && r.Content == content {
			return true
		}
	}
	return false
}

func (f *fakeDNS) ListUnder(_ context.Context, sub string) ([]cloudflare.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var under []cloudflare.Record
	for _, r := range f.records {
		if r.Name == sub || strings.HasSuffix(r.Name, "."+sub) {
			under = append(under, r)
		}
	}
	return under, nil
}

func (f *fakeDNS) DeleteUnder(_ context.Context, sub string) ([]cloudflare.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept, gone []cloudflare.Record
	for _, r := range f.records {
		if r.Name == sub || strings.HasSuffix(r.Name, "."+sub) {
			gone = append(gone, r)
		} else {
			kept = append(kept, r)
		}
	}
	f.records = kept
	return gone, nil
}

func (f *fakeDNS) RunRecords(context.Context) ([]cloudflare.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cloudflare.Record
	for _, r := range f.records {
		if _, ok := f.RunIDOf(r.Name); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDNS) DeleteRecords(_ context.Context, recs []cloudflare.Record) ([]cloudflare.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	drop := map[string]bool{}
	for _, r := range recs {
		drop[r.ID] = true
	}
	var kept []cloudflare.Record
	for _, r := range f.records {
		if !drop[r.ID] {
			kept = append(kept, r)
		}
	}
	f.records = kept
	return recs, nil
}

// fakeCmd plays the local tools: go, git, tar, bash and the orama CLI.
type fakeCmd struct {
	mu     sync.Mutex
	t      *testing.T
	calls  []command
	joined []string // node IPs that ran node setup
	// envOf is the --env each joined IP was set up in.
	envOf map[string]string
	// fail makes a call whose command line contains the key fail with the exit.
	fail map[string]int
	// slotIP, when set, replaces the IP the cluster reports for a slot.
	slotIP  string
	healthy bool
	// flaky makes the next calls whose command line contains the key fail
	// with the listed exits, one per call, before they answer normally.
	flaky map[string][]int
	// leader, when set, is the RQLite leader the report names.
	leader string
}

func (f *fakeCmd) Run(_ context.Context, c command) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	line := c.String()
	for key, code := range f.fail {
		if strings.Contains(line, key) {
			return "", &cmdError{cmd: line, exit: code, tail: "scripted failure", err: errors.New("exit status")}
		}
	}
	for key, codes := range f.flaky {
		if strings.Contains(line, key) && len(codes) > 0 {
			f.flaky[key] = codes[1:]
			return "", &cmdError{cmd: line, exit: codes[0], tail: "scripted flake", err: errors.New("exit status")}
		}
	}
	return f.answer(c)
}

func (f *fakeCmd) answer(c command) (string, error) {
	args := strings.Join(c.args, " ")
	switch {
	case c.name == "go" && strings.HasPrefix(args, "env"):
		return "/gopath\n/gopath/pkg/mod\n/gocache\n", nil
	case strings.HasPrefix(args, "build --output "):
		return "", os.WriteFile(c.args[2], []byte("archive"), 0o600)
	case strings.HasPrefix(args, "node setup "):
		f.joined = append(f.joined, c.args[3])
		if f.envOf == nil {
			f.envOf = map[string]string{}
		}
		f.envOf[c.args[3]] = argAfter(c.args, "--env")
		return "", nil
	case strings.HasPrefix(args, "node dns delegation"):
		return f.slots(argAfter(c.args, "--env")), nil
	case strings.HasPrefix(args, "status report") && strings.Contains(args, "--node "):
		return `{"meta":{"node_count":1,"healthy_count":1},"summary":{"rqlite_leader":"none"},"alerts":[]}`, nil
	case strings.HasPrefix(args, "status report"):
		return f.report(), nil
	}
	return "", nil
}

// slots are the nameserver slots of env: the nodes set up in it, under the
// domain named like the environment.
func (f *fakeCmd) slots(env string) string {
	var nss []cloudflare.Nameserver
	for _, ip := range f.joined {
		if f.envOf[ip] != env {
			continue
		}
		if f.slotIP != "" {
			ip = f.slotIP
		}
		nss = append(nss, cloudflare.Nameserver{Hostname: fmt.Sprintf("ns%d", len(nss)+1), IP: ip})
	}
	if len(nss) == 0 {
		return ""
	}
	raw, _ := json.Marshal([]delegation{{Domain: env + "." + testZone, Nameservers: nss}})
	return string(raw)
}

func (f *fakeCmd) report() string {
	healthy := len(f.joined)
	if !f.healthy {
		healthy--
	}
	leader := f.leader
	if leader == "" {
		leader = "203.0.113.3"
	}
	return fmt.Sprintf(`{"meta":{"node_count":%d,"healthy_count":%d},"summary":{"rqlite_leader":%q,`+
		`"rqlite_quorum":"ok","wg_mesh_status":"ok","critical_alerts":0},"alerts":[]}`, len(f.joined), healthy, leader)
}

func (f *fakeCmd) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.String())
	}
	return out
}

// testEnv is a run against fakes, with every wait short.
type testEnv struct {
	cfg        Config
	cloud      *fakeCloud
	dns        *fakeDNS
	cmd        *fakeCmd
	remote     *fakeRemote
	agentStops int
	certErr    error
	d          deps
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	repo := t.TempDir()
	writeRepoRoots(t, repo)
	work := t.TempDir()
	e := &testEnv{
		cfg: Config{RunID: "testrun1", WorkDir: work, ArtifactDir: filepath.Join(work, "artifacts"), RepoRoot: repo,
			Location: DefaultLocation, ServerType: DefaultServerType, Image: DefaultImage,
			HetznerToken: "hcloud-secret-value", CFToken: "cf-secret-value", CFZone: testZone,
			RWAgentBin: "/bin/true", RWBin: "/bin/true", ServerLimit: defaultServerLimit, TTL: DefaultTTL,
			RunnerCIDRs:   []string{"198.51.100.0/24"},
			EpochDuration: DefaultEpochDuration, EpochMinBlocks: DefaultEpochMinBlocks},
		cloud: newFakeCloud(), dns: &fakeDNS{}, cmd: &fakeCmd{t: t, healthy: true, fail: map[string]int{}},
	}
	e.remote = &fakeRemote{cloud: e.cloud, wgIP: wgFromPublic}
	e.d = deps{cloud: e.cloud, dns: e.dns, cmd: e.cmd, remote: e.remote,
		startAgent: e.startAgent, stopAgentDir: stopDirForTest,
		waitCert:      func(context.Context, string, string, string, time.Duration) error { return e.certErr },
		verifyArchive: func(string, string) error { return errors.New("signed by 0xsomeoneelse") },
		timing:        fastTiming()}
	return e
}

func fastTiming() timing {
	ms := 20 * time.Millisecond
	return timing{poll: ms, serverBoot: time.Second, serverGone: time.Second, sshReady: time.Second,
		delegation: 300 * time.Millisecond, certificate: time.Second, health: 300 * time.Millisecond, remoteAction: time.Second}
}

func (e *testEnv) startAgent(_ context.Context, _ agent.StartConfig) (*agentRun, error) {
	dir, err := os.MkdirTemp(e.cfg.WorkDir, agent.DirPrefix)
	if err != nil {
		return nil, err
	}
	return &agentRun{Dir: dir, Sock: filepath.Join(dir, "a.sock"), Address: "0x2222222222222222222222222222222222222222",
		Stop: func() error { e.agentStops++; return os.RemoveAll(dir) }}, nil
}

func stopDirForTest(_ context.Context, dir string) error { return os.RemoveAll(dir) }

type testLogger struct{ lines []string }

func (l *testLogger) Infof(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}
