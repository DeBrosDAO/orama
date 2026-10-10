package relupgrade

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

func testTarget() Target {
	return Target{
		Network: "stagenet",
		Manifest: &netregistry.Manifest{
			Name: "stagenet", Channel: "nightly", ReleaseRepo: "https://releases.example",
			ReleaseRootSHA256: strings.Repeat("a", 64), MinVersion: "0.3.0",
		},
		Root: []byte(`{"root":true}`),
	}
}

// fleet is a fake cluster and the log of what was done to it.
type fleet struct {
	mu       sync.Mutex // the staging runs in goroutines
	nodes    []inspector.Node
	roles    map[string]rollout.RaftRole
	versions map[string]NodeState
	machine  map[string]string

	fetched []string
	staged  []string
	rolled  []string
	global  []string
	events  []string

	version     string
	stageErr    map[string]error
	fetchErr    error
	statesErr   error
	rollErr     error
	closed      bool
	rolledAfter []string
}

func newFleet(versions map[string]string) *fleet {
	f := &fleet{
		roles:    map[string]rollout.RaftRole{},
		versions: map[string]NodeState{},
		machine:  map[string]string{},
		stageErr: map[string]error{},
		version:  "0.4.0",
	}
	for _, h := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		f.nodes = append(f.nodes, inspector.Node{Host: h, User: "root", Role: "node"})
		f.roles[h] = rollout.RoleFollower
		f.machine[h] = "x86_64"
		f.versions[h] = NodeState{Version: versions[h], Global: h == "10.0.0.2"}
	}
	f.roles["10.0.0.3"] = rollout.RoleLeader
	return f
}

func (f *fleet) Connect() ([]inspector.Node, func(), error) {
	return f.nodes, func() { f.closed = true }, nil
}

func (f *fleet) Plan(nodes []inspector.Node) (*rollout.Plan, error) {
	return rollout.Build(nodes, f.roles)
}

func (f *fleet) Roll(plan *rollout.Plan, after func(inspector.Node) error) error {
	for _, s := range plan.Steps {
		f.events = append(f.events, "roll "+s.Node.Host)
		f.rolled = append(f.rolled, s.Node.Host)
		if f.rollErr != nil {
			return f.rollErr
		}
		if err := after(s.Node); err != nil {
			return err
		}
	}
	return nil
}

func (f *fleet) runner(opts Options) (*runner, *bytes.Buffer) {
	var out bytes.Buffer
	opts.Out = &out
	if opts.In == nil {
		opts.In = strings.NewReader("yes\n")
	}
	return &runner{
		opts: opts, env: "stagenet", target: testTarget(), home: "/home/test", now: time.Unix(1, 0), roller: f,
		seams: seams{
			arch: func(n inspector.Node) (string, error) { return f.machine[n.Host], nil },
			fetch: func(_ context.Context, p releasefetch.Params) (*releasefetch.Release, error) {
				f.fetched = append(f.fetched, p.Arch)
				if f.fetchErr != nil {
					return nil, f.fetchErr
				}
				return &releasefetch.Release{Version: f.version, Target: "nightly/orama-" + p.Arch, ArchivePath: "/w/a.tar.gz", MetadataDir: "/w/metadata", Root: p.Root}, nil
			},
			states: func(context.Context, string, bool) (map[string]NodeState, error) { return f.versions, f.statesErr },
			stage: func(n inspector.Node, _ push.ReleaseFiles) (string, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.events = append(f.events, "stage "+n.Host)
				f.staged = append(f.staged, n.Host)
				return "", f.stageErr[n.Host]
			},
			global: func(n inspector.Node) error {
				f.global = append(f.global, n.Host)
				return nil
			},
		},
	}, &out
}

func TestRun_upgradesOutdatedNodesAfterStagingEveryNode(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.3.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.4.0"})
	r, out := f.runner(Options{})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if want := []string{"10.0.0.1", "10.0.0.2"}; !slices.Equal(f.rolled, want) {
		t.Errorf("rolled %v, want %v: the node already on the release is left alone", f.rolled, want)
	}
	firstRoll := slices.IndexFunc(f.events, func(e string) bool { return strings.HasPrefix(e, "roll ") })
	stages := 0
	for _, e := range f.events[:firstRoll] {
		if strings.HasPrefix(e, "stage ") {
			stages++
		}
	}
	if stages != 2 {
		t.Errorf("%d nodes staged before the first restart, want 2: %v", stages, f.events)
	}
	if want := []string{"10.0.0.1", "10.0.0.2"}; !slices.Equal(f.global, want) {
		t.Errorf("global layer refresh ran for %v, want after every upgraded node %v", f.global, want)
	}
	if !f.closed {
		t.Error("the SSH keys were not cleaned up")
	}
	if !strings.Contains(out.String(), "0.4.0") || !strings.Contains(out.String(), "upgrade") {
		t.Errorf("plan output lacks the target version and actions:\n%s", out)
	}
}

func TestRun_dryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.3.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.3.0"})
	r, out := f.runner(Options{DryRun: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(f.staged)+len(f.rolled)+len(f.global) != 0 {
		t.Errorf("a dry run staged %v, restarted %v and refreshed %v", f.staged, f.rolled, f.global)
	}
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "0.3.0", "0.4.0", "Leader"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the plan does not show %q:\n%s", want, out)
		}
	}
}

func TestRun_everyNodeCurrentDoesNothing(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.4.0", "10.0.0.2": "0.4.0", "10.0.0.3": "0.4.0"})
	r, out := f.runner(Options{})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(f.staged)+len(f.rolled) != 0 {
		t.Errorf("staged %v and restarted %v nodes that already run the release", f.staged, f.rolled)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("output does not say nothing is to be done:\n%s", out)
	}
}

func TestRun_reinstallRollsNodesOnTheRelease(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.4.0", "10.0.0.2": "0.4.0", "10.0.0.3": "0.4.0"})
	r, _ := f.runner(Options{Reinstall: true, Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}; !slices.Equal(f.rolled, want) {
		t.Errorf("rolled %v, want followers then the leader %v", f.rolled, want)
	}
}

func TestRun_neverDowngradesANewerNode(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.9.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.9.0"})
	r, out := f.runner(Options{Yes: true, Reinstall: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if want := []string{"10.0.0.2"}; !slices.Equal(f.rolled, want) {
		t.Errorf("rolled %v, want only the older node %v", f.rolled, want)
	}
	if !strings.Contains(out.String(), "left alone") {
		t.Errorf("output does not say the newer nodes are left alone:\n%s", out)
	}
}

func TestRun_declinedConfirmationChangesNothing(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.3.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.3.0"})
	r, _ := f.runner(Options{In: strings.NewReader("no\n")})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeAborted {
		t.Fatalf("err = %v, want an aborted confirmation", err)
	}
	if len(f.staged)+len(f.rolled) != 0 {
		t.Errorf("staged %v and restarted %v after the operator declined", f.staged, f.rolled)
	}
}

func TestRun_aRefusedStageRestartsNothing(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.3.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.3.0"})
	f.stageErr["10.0.0.2"] = errors.New("trusts another release root")
	r, out := f.runner(Options{Yes: true})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "trusts another release root") {
		t.Fatalf("err = %v, want the node's refusal", err)
	}
	if len(f.rolled) != 0 {
		t.Errorf("restarted %v after a node refused the release", f.rolled)
	}
	if !strings.Contains(out.String(), "10.0.0.2") {
		t.Errorf("the refusing node is not named:\n%s", out)
	}
}

func TestRun_fetchFailureChangesNothing(t *testing.T) {
	f := newFleet(map[string]string{})
	f.fetchErr = errors.New("snapshot rolled back")
	r, _ := f.runner(Options{Yes: true})

	if err := r.run(context.Background()); err == nil || !strings.Contains(err.Error(), "snapshot rolled back") {
		t.Fatalf("err = %v, want the fetch failure", err)
	}
	if len(f.staged)+len(f.rolled) != 0 {
		t.Errorf("staged %v and restarted %v without a release", f.staged, f.rolled)
	}
}

func TestRun_telemetryFailureSaysHowToReadOverSSH(t *testing.T) {
	f := newFleet(map[string]string{})
	f.statesErr = errors.New("gateway down")
	r, _ := f.runner(Options{Yes: true})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(err.Error(), "--ssh") {
		t.Fatalf("err = %v, want unavailable with the --ssh hint", err)
	}
	if len(f.rolled) != 0 {
		t.Errorf("restarted %v with no idea what the nodes run", f.rolled)
	}
}

func TestRun_unknownMachineTypeIsRefused(t *testing.T) {
	f := newFleet(map[string]string{})
	f.machine["10.0.0.1"] = "riscv64"
	r, _ := f.runner(Options{Yes: true})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("err = %v, want the machine type named", err)
	}
}

func TestRun_fetchesOneReleasePerArchitecture(t *testing.T) {
	f := newFleet(map[string]string{"10.0.0.1": "0.3.0", "10.0.0.2": "0.3.0", "10.0.0.3": "0.3.0"})
	f.machine["10.0.0.2"] = "aarch64"
	r, _ := f.runner(Options{Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	slices.Sort(f.fetched)
	if want := []string{"amd64", "arm64"}; !slices.Equal(f.fetched, want) {
		t.Errorf("fetched %v, want one per architecture %v", f.fetched, want)
	}
}

func TestRun_architecturesOnDifferentVersionsAreRefused(t *testing.T) {
	f := newFleet(map[string]string{})
	f.machine["10.0.0.2"] = "aarch64"
	r, _ := f.runner(Options{Yes: true})
	calls := 0
	inner := r.seams.fetch
	r.seams.fetch = func(ctx context.Context, p releasefetch.Params) (*releasefetch.Release, error) {
		rel, err := inner(ctx, p)
		if calls++; calls == 2 && rel != nil {
			rel.Version = "0.4.1"
		}
		return rel, err
	}

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeConflict {
		t.Fatalf("err = %v, want a conflict while the release is half published", err)
	}
	if len(f.rolled) != 0 {
		t.Errorf("restarted %v", f.rolled)
	}
}

func TestResolveTarget_networkOfTheRegistry(t *testing.T) {
	reg := registryWith(t, "stagenet")
	got, err := ResolveTarget(&cli.Environment{Name: "main", Network: "stagenet"}, reg)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Network != "stagenet" || got.Manifest.Channel != "nightly" || len(got.Root) == 0 {
		t.Errorf("target = %+v", got)
	}
}

func TestResolveTarget_environmentOnNoNetwork(t *testing.T) {
	_, err := ResolveTarget(&cli.Environment{Name: "mine"}, registryWith(t, "stagenet"))
	if err == nil || !strings.Contains(err.Error(), "maint rollout") {
		t.Fatalf("err = %v, want the way to upgrade from your own build", err)
	}
}

func TestResolveTarget_unknownNetwork(t *testing.T) {
	_, err := ResolveTarget(&cli.Environment{Name: "main", Network: "nope"}, registryWith(t, "stagenet"))
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want the unknown network named", err)
	}
}

func TestFetchParams_keepsTheRollbackRecordPerNetwork(t *testing.T) {
	p := testTarget().FetchParams("/home/u/.orama", "arm64", time.Unix(5, 0))

	if p.SeenPath != "/home/u/.orama/releases/stagenet/release-seen.json" || p.Arch != "arm64" ||
		p.RepoURL != "https://releases.example" || p.Channel != "nightly" || p.MinVersion != "0.3.0" {
		t.Errorf("params = %+v", p)
	}
}
