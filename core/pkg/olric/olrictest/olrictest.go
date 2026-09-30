// Package olrictest starts a real Olric (one member, or a cluster) for tests, so cache
// behaviour (expiry above all) is exercised against Olric itself rather than
// against a mock that only records the arguments it was given.
package olrictest

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"testing"
	"time"

	olriclib "github.com/olric-data/olric"
	"github.com/olric-data/olric/config"
)

const (
	// startTimeout bounds how long a test waits for the member to come up.
	startTimeout = 5 * time.Second

	// clusterFormTimeout bounds how long a test waits for every member to see
	// the whole cluster and own partitions; joining and the first routing
	// table distribution take a few hundred milliseconds on loopback.
	clusterFormTimeout = 15 * time.Second

	// clusterPollInterval is how often the cluster is asked whether it formed.
	clusterPollInterval = 50 * time.Millisecond
)

// Server is one running Olric member.
type Server struct {
	// Addr is the member's client address, for olriclib.NewClusterClient.
	Addr string
	db   *olriclib.Olric
}

// EmbeddedClient returns an in-process client, the kind the serverless host
// functions are handed on a node.
func (s *Server) EmbeddedClient() olriclib.Client {
	return s.db.NewEmbeddedClient()
}

// Stop shuts the member down now, so a test can see how a client fails when the
// cache goes away. The shutdown at the end of the test is then a no-op.
func (s *Server) Stop(t *testing.T) {
	t.Helper()
	if err := s.db.Shutdown(context.Background()); err != nil {
		t.Fatalf("olrictest: failed to stop the member: %v", err)
	}
}

// Start runs Olric on a free loopback port and stops it when the test ends.
func Start(t *testing.T) *Server {
	t.Helper()
	return startMember(t, 0, nil)
}

// StartCluster runs n members that form one cluster, so a key's partition can
// be owned by a member other than the one a client asked: the case a single
// member never exercises.
func StartCluster(t *testing.T, n int) []*Server {
	t.Helper()
	first, err := freePort()
	if err != nil {
		t.Fatalf("olrictest: %v", err)
	}
	seed := fmt.Sprintf("127.0.0.1:%d", first)
	members := []*Server{startMember(t, first, nil)}
	for i := 1; i < n; i++ {
		members = append(members, startMember(t, 0, []string{seed}))
	}
	waitForCluster(t, members)
	return members
}

// waitForCluster fails the test unless, within clusterFormTimeout, every member
// reports all n members and owns partitions. A cluster that has not formed
// answers from one member, which is the single-member case a cluster test
// exists to get away from.
func waitForCluster(t *testing.T, members []*Server) {
	t.Helper()
	deadline := time.Now().Add(clusterFormTimeout)
	var last string
	for {
		last = clusterGap(members)
		if last == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("olrictest: the %d-member cluster did not form within %s: %s", len(members), clusterFormTimeout, last)
		}
		time.Sleep(clusterPollInterval)
	}
}

// clusterGap says what is missing from a formed cluster, or "" when nothing is.
func clusterGap(members []*Server) string {
	ctx, cancel := context.WithTimeout(context.Background(), clusterFormTimeout)
	defer cancel()
	for _, m := range members {
		st, err := m.EmbeddedClient().Stats(ctx, m.Addr)
		if err != nil {
			return fmt.Sprintf("member %s cannot report its stats: %v", m.Addr, err)
		}
		if len(st.ClusterMembers) != len(members) {
			return fmt.Sprintf("member %s sees %d of %d members", m.Addr, len(st.ClusterMembers), len(members))
		}
		if len(st.Partitions) == 0 {
			return fmt.Sprintf("member %s owns no partitions yet", m.Addr)
		}
	}
	return ""
}

// startMember runs one member. memberlistPort 0 picks a free one; peers are
// the memberlist addresses it joins.
func startMember(t *testing.T, memberlistPort int, peers []string) *Server {
	t.Helper()

	port, err := freePort()
	if err != nil {
		t.Fatalf("olrictest: %v", err)
	}

	c := config.New("local")
	c.BindAddr = "127.0.0.1"
	c.BindPort = port
	c.MemberlistConfig.BindAddr = "127.0.0.1"
	c.MemberlistConfig.BindPort = memberlistPort
	c.Peers = peers
	c.Logger = log.New(io.Discard, "", 0)

	started := make(chan struct{})
	c.Started = func() { close(started) }

	db, err := olriclib.New(c)
	if err != nil {
		t.Fatalf("olrictest: failed to create Olric: %v", err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- db.Start() }()
	t.Cleanup(func() { _ = db.Shutdown(context.Background()) })

	select {
	case <-started:
	case err := <-startErr:
		t.Fatalf("olrictest: Olric failed to start on 127.0.0.1:%d: %v", port, err)
	case <-time.After(startTimeout):
		t.Fatalf("olrictest: Olric did not start within %s", startTimeout)
	}

	return &Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), db: db}
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to reserve a port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		return 0, fmt.Errorf("failed to release reserved port %d: %w", port, err)
	}
	return port, nil
}
