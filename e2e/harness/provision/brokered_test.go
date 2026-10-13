package provision

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// st2time is a record creation time old enough for nothing to care.
func st2time() time.Time { return time.Now().Add(-time.Hour) }

// recordingCloud is the broker's cloud, recording what it was asked.
type recordingCloud struct{ ops []string }

func (c *recordingCloud) AddExtra(_ context.Context, _ *fleet.State, name, loc string) (fleet.Node, error) {
	c.ops = append(c.ops, "add "+name)
	return fleet.Node{Name: name, PublicIP: "203.0.113.77", Location: loc}, nil
}

func (c *recordingCloud) RemoveExtra(_ context.Context, _ *fleet.State, name string) error {
	c.ops = append(c.ops, "remove "+name)
	return nil
}

func (c *recordingCloud) AddCluster(_ context.Context, _ *fleet.State, name string) (fleet.Cluster, error) {
	c.ops = append(c.ops, "cluster "+name)
	return fleet.Cluster{Name: name}, nil
}

func (c *recordingCloud) RemoveCluster(_ context.Context, _ *fleet.State, name string) error {
	c.ops = append(c.ops, "uncluster "+name)
	return nil
}

// noTXT is a broker zone that holds nothing.
type noTXT struct{ fakeDNS }

func (*noTXT) SetTXT(context.Context, string, string) error { return nil }
func (*noTXT) DeleteTXT(context.Context, string, string) ([]cloudflare.Record, error) {
	return nil, nil
}

// startTestBroker serves a broker for run testrun1 and returns its socket.
func startTestBroker(t *testing.T, cloud broker.Cloud) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pbrk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	srv := &broker.Server{State: &fleet.State{RunID: "testrun1"}, DNS: &noTXT{}, Cloud: cloud}
	l, err := broker.Listen(context.Background(), dir, srv, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return l.Path
}

func (f *fakeDNS) ClusterSubdomain(runID, label string) (string, error) {
	if !runIDPattern.MatchString(runID) || !evalNamePattern.MatchString(label) {
		return "", fmt.Errorf("bad run id %q or label %q", runID, label)
	}
	return namePrefix + runID + "-" + label + "." + testZone, nil
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}
