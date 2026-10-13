package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

func TestNewSource_configWithoutSSHIsAUsageError(t *testing.T) {
	_, err := NewSource(Options{Env: "devnet", ConfigPath: "/tmp/nodes.conf"})
	if err == nil {
		t.Fatal("--config without --ssh was accepted")
	}
	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--ssh") {
		t.Fatalf("want a usage error naming --ssh, got code %d: %v", clierr.CodeOf(err), err)
	}
}

func TestNewSource_sshOnlyWhenAsked(t *testing.T) {
	src, err := NewSource(Options{Env: "devnet", SSH: true, ConfigPath: "/tmp/nodes.conf"})
	if err != nil {
		t.Fatalf("NewSource(--ssh): %v", err)
	}
	if src.Mode() != ModeSSH {
		t.Fatalf("mode = %s, want ssh", src.Mode())
	}
}

func TestNewSource_apiIsTheDefault(t *testing.T) {
	isolateHome(t)
	src, err := NewSource(Options{Env: "unit"})
	if err != nil {
		t.Fatalf("NewSource(unit): %v", err)
	}
	if src.Mode() != ModeAPI {
		t.Fatalf("mode = %s, want api", src.Mode())
	}
}

func TestNewSource_unknownEnvironmentSuggestsSSH(t *testing.T) {
	isolateHome(t)
	_, err := NewSource(Options{Env: "no-such-env"})
	if err == nil {
		t.Fatal("an unknown environment was accepted")
	}
	if !strings.Contains(err.Error(), "--ssh") || !strings.Contains(err.Error(), "no-such-env") {
		t.Fatalf("error does not name the environment and the escape hatch: %v", err)
	}
}

func TestResolveInterval_bounds(t *testing.T) {
	cases := []struct {
		name      string
		requested time.Duration
		explicit  bool
		ssh       bool
		want      time.Duration
		wantErr   bool
	}{
		{"api default", DefaultInterval, false, false, DefaultInterval, false},
		{"api minimum", MinAPIInterval, true, false, MinAPIInterval, false},
		{"api below minimum", time.Second, true, false, 0, true},
		{"api zero", 0, true, false, 0, true},
		{"api maximum", MaxAPIInterval, true, false, MaxAPIInterval, false},
		{"api above maximum", 2 * time.Minute, true, false, 0, true},
		{"ssh default is raised", DefaultInterval, false, true, MinSSHInterval, false},
		{"ssh explicit below minimum", 5 * time.Second, true, true, 0, true},
		{"ssh explicit above minimum", time.Minute, true, true, time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveInterval(tc.requested, tc.explicit, tc.ssh)
			if tc.wantErr {
				if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
					t.Fatalf("want a usage error, got %v, %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func threeNodeSnapshot() *cluster.ClusterSnapshot {
	return &cluster.ClusterSnapshot{
		Environment: "devnet",
		Nodes: []cluster.CollectionStatus{
			{Node: cluster.NodeRef{Host: "1.1.1.1", WGIP: "10.0.0.1"}},
			{Node: cluster.NodeRef{Host: "2.2.2.2", WGIP: "10.0.0.2"}},
			{Node: cluster.NodeRef{Host: "3.3.3.3"}},
		},
		Alerts: []cluster.Alert{
			{Severity: cluster.AlertCritical, Node: "2.2.2.2", Message: "down"},
			{Severity: cluster.AlertWarning, Node: "1.1.1.1", Message: "disk"},
			{Severity: cluster.AlertCritical, Node: "cluster", Message: "no leader"},
		},
	}
}

func TestFilterNode_byPublicHost(t *testing.T) {
	snap := threeNodeSnapshot()
	got, err := FilterNode(snap, "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Node.Host != "2.2.2.2" {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	if len(got.Alerts) != 1 || got.Alerts[0].Message != "down" {
		t.Fatalf("alerts = %+v, want only the node's own", got.Alerts)
	}
	if len(snap.Nodes) != 3 || len(snap.Alerts) != 3 {
		t.Fatal("filtering changed the original snapshot")
	}
}

func TestFilterNode_byWireGuardIP(t *testing.T) {
	got, err := FilterNode(threeNodeSnapshot(), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[0].Node.Host != "1.1.1.1" || len(got.Alerts) != 1 || got.Alerts[0].Message != "disk" {
		t.Fatalf("got %+v", got)
	}
}

func TestFilterNode_unknownNodeNamesTheKnownOnes(t *testing.T) {
	_, err := FilterNode(threeNodeSnapshot(), "9.9.9.9")
	if err == nil {
		t.Fatal("an unknown node was accepted")
	}
	if clierr.CodeOf(err) != clierr.CodeNotFound || !strings.Contains(err.Error(), "1.1.1.1, 2.2.2.2, 3.3.3.3") {
		t.Fatalf("got code %d: %v", clierr.CodeOf(err), err)
	}
}

func TestFilterNode_emptySnapshot(t *testing.T) {
	if _, err := FilterNode(&cluster.ClusterSnapshot{}, "1.1.1.1"); err == nil {
		t.Fatal("an empty snapshot matched a node")
	}
}

// fakeSource returns a fixed snapshot and replays fixed updates.
type fakeSource struct {
	snap    *cluster.ClusterSnapshot
	updates []Update
}

func (f *fakeSource) Mode() Mode { return ModeAPI }

func (f *fakeSource) Snapshot(context.Context) (*cluster.ClusterSnapshot, error) {
	return f.snap, nil
}

func (f *fakeSource) Watch(ctx context.Context, _ time.Duration) <-chan Update {
	out := make(chan Update)
	go func() {
		defer close(out)
		for _, u := range f.updates {
			if !send(ctx, out, u) {
				return
			}
		}
	}()
	return out
}

func TestScopedSource_namesTheEnvironmentAndFilters(t *testing.T) {
	s := &scopedSource{inner: &fakeSource{snap: threeNodeSnapshot()}, env: "testnet", node: "3.3.3.3"}
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Environment != "testnet" || len(snap.Nodes) != 1 {
		t.Fatalf("got env %q with %d nodes", snap.Environment, len(snap.Nodes))
	}
}

func TestScopedSource_watchScopesEverySnapshot(t *testing.T) {
	inner := &fakeSource{updates: []Update{
		{State: LinkConnecting},
		{State: LinkLive, Snapshot: threeNodeSnapshot()},
	}}
	s := &scopedSource{inner: inner, env: "devnet", node: "9.9.9.9"}
	var got []Update
	for u := range s.Watch(context.Background(), time.Second) {
		got = append(got, u)
	}
	if len(got) != 2 || got[0].State != LinkConnecting {
		t.Fatalf("updates = %+v", got)
	}
	if got[1].Snapshot != nil || got[1].Err == nil {
		t.Fatalf("a snapshot without the requested node was passed on: %+v", got[1])
	}
}

// Retrying cannot fix a --config file that does not exist, so the SSH live
// view stops instead of failing every interval forever.
func TestSSHWatch_unreadableConfigStops(t *testing.T) {
	s := &sshSource{cfg: CollectorConfig{Env: "devnet", ConfigPath: t.TempDir() + "/missing.conf"}}
	var got []Update
	for u := range s.Watch(context.Background(), time.Hour) {
		got = append(got, u)
	}
	if len(got) != 1 || got[0].State != LinkFailed || clierr.CodeOf(got[0].Err) != clierr.CodeUsage {
		t.Fatalf("updates = %+v", got)
	}
}

// With --ssh every node is collected and --node filters afterwards, so a node
// can be named by its WireGuard IP there too.
func TestNewSource_sshFiltersAfterCollection(t *testing.T) {
	src, err := NewSource(Options{Env: "devnet", SSH: true, Node: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	scoped := src.(*scopedSource)
	if scoped.node != "10.0.0.2" {
		t.Fatalf("node filter = %q", scoped.node)
	}
	if got, err := scoped.scope(threeNodeSnapshot()); err != nil || got.Nodes[0].Node.Host != "2.2.2.2" {
		t.Fatalf("a WireGuard IP did not select its node: %+v, %v", got, err)
	}
}
