package namespace

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func confirmRig(t *testing.T, served []string, remote func(nodeIP string) error) (*ClusterManager, *[]string) {
	t.Helper()
	var asked []string
	cm := &ClusterManager{
		logger:      zap.NewNop(),
		localNodeID: "local",
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
