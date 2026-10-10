package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

func confirmRig(t *testing.T, served []string, remote func(nodeIP string) error) (*ClusterManager, *[]string) {
	t.Helper()
	var asked []string
	cm := &ClusterManager{
		logger:                zap.NewNop(),
		localNodeID:           "local",
		waitHostTURNServingFn: func(context.Context, string) error { return nil },
		reconcileHostTURNFn: func(context.Context) ([]string, error) {
			return served, nil
		},
		spawnRequestFn: func(_ context.Context, ip string, req map[string]interface{}) (*spawnResponse, error) {
			if req["action"] != spawnActionReconcileHostTURN {
				t.Errorf("action = %v, want %s", req["action"], spawnActionReconcileHostTURN)
			}
			if req["node_id"] == "" || req["namespace"] != "acme" {
				t.Errorf("request %v names no node or the wrong namespace", req)
			}
			asked = append(asked, ip)
			if err := remote(ip); err != nil {
				return nil, err
			}
			return &spawnResponse{Success: true}, nil
		},
	}
	return cm, &asked
}

var confirmTURNNodes = []clusterNodeInfo{
	{NodeID: "local", InternalIP: "10.0.0.1", PublicIP: "1.1.1.1"},
	{NodeID: "n2", InternalIP: "10.0.0.2", PublicIP: "2.2.2.2"},
}

// Stagenet e2e run 21: enabling WebRTC advertised every TURN node at once, but
// a remote host applied its tenant set only on its next sweep; its relay refused
// the namespace's credentials and the call never connected.
func TestConfirmTURNHosts_advertisesOnlyHostsThatConfirmed(t *testing.T) {
	cm, asked := confirmRig(t, []string{"acme"}, func(ip string) error {
		return errors.New("spawn request failed on " + ip + ": does not serve")
	})
	got := cm.confirmTURNHosts(context.Background(), "acme", confirmTURNNodes, []string{"acme"})
	if want := []string{"1.1.1.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised %v, want %v: the remote host did not confirm", got, want)
	}
	if want := []string{"10.0.0.2"}; !reflect.DeepEqual(*asked, want) {
		t.Fatalf("asked %v, want %v: the local host is not asked over the wire", *asked, want)
	}
}

func TestConfirmTURNHosts_everyHostConfirming(t *testing.T) {
	cm, _ := confirmRig(t, nil, func(string) error { return nil })
	got := cm.confirmTURNHosts(context.Background(), "acme", confirmTURNNodes, []string{"acme"})
	if want := []string{"1.1.1.1", "2.2.2.2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised %v, want %v", got, want)
	}
}

func TestConfirmTURNHosts_aLocalHostNotServingIsNotAdvertised(t *testing.T) {
	cm, _ := confirmRig(t, nil, func(string) error { return nil })
	got := cm.confirmTURNHosts(context.Background(), "acme", confirmTURNNodes, []string{"other"})
	if want := []string{"2.2.2.2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised %v, want %v", got, want)
	}
}

func TestConfirmTURNHosts_noTURNNodes(t *testing.T) {
	cm, _ := confirmRig(t, nil, func(string) error { return nil })
	if got := cm.confirmTURNHosts(context.Background(), "acme", nil, []string{"acme"}); len(got) != 0 {
		t.Fatalf("advertised %v with no TURN nodes", got)
	}
}

func TestConfirmHostTURN(t *testing.T) {
	cm := &ClusterManager{}
	cm.waitHostTURNServingFn = func(context.Context, string) error { return nil }
	cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return []string{"acme", "other"}, nil }
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err != nil {
		t.Fatalf("a served namespace: %v", err)
	}
	err := cm.ConfirmHostTURN(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want a refusal naming the namespace this host does not serve", err)
	}
	cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return nil, errors.New("config write failed") }
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err == nil || !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("err = %v, want the reconcile failure", err)
	}
}

// liveTURNRig points the shared config at a temp dir holding a written config,
// and shortens the wait so a refusal is quick.
func liveTURNRig(t *testing.T) (cm *ClusterManager, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "turn.yaml")
	if err := os.WriteFile(path, []byte("tenants: written\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(swapHostTURNConfigPath(t, path))
	prevServed := hostTURNServedPath
	hostTURNServedPath = filepath.Join(t.TempDir(), "served-tenants.json")
	t.Cleanup(func() { hostTURNServedPath = prevServed })
	prev := hostTURNServeTimeout
	hostTURNServeTimeout = 600 * time.Millisecond
	t.Cleanup(func() { hostTURNServeTimeout = prev })
	cm = &ClusterManager{
		logger:              zap.NewNop(),
		reconcileHostTURNFn: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
		hostTURNActiveFn:    func() (bool, error) { return true, nil },
	}
	return cm, path
}

func publishServed(t *testing.T, configPath, digestOf string, namespaces ...string) {
	t.Helper()
	data, err := json.Marshal(turn.ServedTenants{Namespaces: namespaces, ConfigSHA256: digestOf})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostTURNServedPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Review M1: the config file being written and the unit being active said
// nothing about the running server, which loads tenants on its next tick.
func TestConfirmHostTURN_refusesWhileTheRunningServerHasNotLoadedTheNamespace(t *testing.T) {
	cm, path := liveTURNRig(t)
	data, _ := os.ReadFile(path)

	// Nothing published yet: the server has not loaded anything.
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err == nil {
		t.Fatal("confirmed with a server that published nothing")
	}
	// Published, but for an older config.
	publishServed(t, path, turn.ConfigDigest([]byte("older")), "acme")
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err == nil {
		t.Fatal("confirmed against a server still serving an older config")
	}
	// Current config, but the namespace is not in what it loaded.
	publishServed(t, path, turn.ConfigDigest(data), "other")
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("err = %v, want a refusal naming the namespace", err)
	}
}

func TestConfirmHostTURN_confirmsOnceTheRunningServerServesIt(t *testing.T) {
	cm, path := liveTURNRig(t)
	data, _ := os.ReadFile(path)
	publishServed(t, path, turn.ConfigDigest(data), "acme", "other")
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err != nil {
		t.Fatalf("a served namespace: %v", err)
	}
}

// The server publishes on its next tick, so the host waits for it.
func TestConfirmHostTURN_waitsForTheNextReloadTick(t *testing.T) {
	cm, path := liveTURNRig(t)
	data, _ := os.ReadFile(path)
	go func() {
		time.Sleep(300 * time.Millisecond)
		publishServed(t, path, turn.ConfigDigest(data), "acme")
	}()
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err != nil {
		t.Fatalf("a server that loads it within the wait: %v", err)
	}
}

// F4: a status file outlives a server that died; its digest still matches the
// config. A dead or crash-looping unit must not confirm.
func TestConfirmHostTURN_aDeadServerWithAnUnchangedDigestIsNotConfirmed(t *testing.T) {
	cm, path := liveTURNRig(t)
	data, _ := os.ReadFile(path)
	publishServed(t, path, turn.ConfigDigest(data), "acme")
	cm.hostTURNActiveFn = func() (bool, error) { return false, nil }
	err := cm.ConfirmHostTURN(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("err = %v, want a refusal because orama-turn is not active", err)
	}
	cm.hostTURNActiveFn = func() (bool, error) { return false, errors.New("systemctl failed") }
	if err := cm.ConfirmHostTURN(context.Background(), "acme"); err == nil || !strings.Contains(err.Error(), "systemctl failed") {
		t.Fatalf("err = %v, want the unreadable state reported", err)
	}
}

func TestConfirmHostTURN_stopsWaitingWhenTheContextEnds(t *testing.T) {
	cm, _ := liveTURNRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cm.ConfirmHostTURN(ctx, "acme"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

// The coordinator's own host is held to the same standard as a remote one.
func TestConfirmTURNHosts_aLocalServerThatHasNotLoadedItIsNotAdvertised(t *testing.T) {
	cm, _ := confirmRig(t, nil, func(string) error { return nil })
	cm.waitHostTURNServingFn = func(context.Context, string) error { return errors.New("not loaded") }
	got := cm.confirmTURNHosts(context.Background(), "acme", confirmTURNNodes, []string{"acme"})
	if want := []string{"2.2.2.2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised %v, want %v", got, want)
	}
}

func TestReleaseHostTURN(t *testing.T) {
	cm := &ClusterManager{}
	cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return []string{"other"}, nil }
	if err := cm.ReleaseHostTURN(context.Background(), "acme"); err != nil {
		t.Fatalf("a namespace the host dropped: %v", err)
	}
	cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return []string{"acme"}, nil }
	if err := cm.ReleaseHostTURN(context.Background(), "acme"); err == nil {
		t.Fatal("a host still serving the namespace reported it released")
	}
	cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return nil, errors.New("rqlite down") }
	if err := cm.ReleaseHostTURN(context.Background(), "acme"); err == nil || !strings.Contains(err.Error(), "rqlite down") {
		t.Fatalf("err = %v, want the reconcile failure", err)
	}
}

// Review L1: a failed enable left every host that had confirmed holding the
// namespace's secret until its next sweep.
func TestCleanupWebRTCOnError_releasesTheTURNHostsThatWereAsked(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	r.cm.reconcileHostTURNFn = func(context.Context) ([]string, error) {
		r.record("local-release")
		return nil, nil
	}
	hosts := []clusterNodeInfo{
		{NodeID: "node-1", InternalIP: "10.0.0.1"},
		{NodeID: "node-2", InternalIP: "10.0.0.2"},
	}
	r.cm.cleanupWebRTCOnError(context.Background(), "cluster-acme", "acme", hosts, hosts)

	if !r.has("local-release") || !r.has("remote:reconcile-host-turn:10.0.0.2") {
		t.Fatalf("not every asked TURN host was told to drop the namespace: %v", r.events)
	}
	if rel, del := r.index("remote:reconcile-host-turn:10.0.0.2"), r.index("db:config-deleted"); rel < del {
		t.Errorf("a host was told to drop the namespace before its config row was deleted: %v", r.events)
	}
}

func TestReleaseTURNHosts_returnsEveryFailureJoined(t *testing.T) {
	r := newDisableRig(t)
	r.cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return nil, errors.New("local broke") }
	r.remoteErr = func(string) error { return errors.New("remote broke") }
	err := r.cm.releaseTURNHosts(context.Background(), "acme", []clusterNodeInfo{
		{NodeID: "node-1", InternalIP: "10.0.0.1"},
		{NodeID: "node-2", InternalIP: "10.0.0.2"},
	})
	if err == nil || !strings.Contains(err.Error(), "local broke") || !strings.Contains(err.Error(), "remote broke") {
		t.Fatalf("err = %v, want both failures", err)
	}
	if err := r.cm.releaseTURNHosts(context.Background(), "acme", nil); err != nil {
		t.Fatalf("no hosts: %v", err)
	}
}

// F5: the rollback runs because the enable failed, often on a cancelled ctx; the
// drop must still reach the hosts.
func TestCleanupWebRTCOnError_releasesEvenWhenTheEnableContextIsCancelled(t *testing.T) {
	r := newDisableRig(t)
	released := false
	r.cm.reconcileHostTURNFn = func(ctx context.Context) ([]string, error) {
		released = ctx.Err() == nil
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hosts := []clusterNodeInfo{{NodeID: "node-1", InternalIP: "10.0.0.1"}}
	r.cm.cleanupWebRTCOnError(ctx, "cluster-acme", "acme", nil, hosts)
	if !released {
		t.Fatal("the release ran under the cancelled enable context")
	}
}
