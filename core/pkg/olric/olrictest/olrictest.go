// Package olrictest starts a real single-member Olric for tests, so cache
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

// startTimeout bounds how long a test waits for the member to come up.
const startTimeout = 5 * time.Second

// Server is a running single-member Olric.
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

// Start runs Olric on a free loopback port and stops it when the test ends.
func Start(t *testing.T) *Server {
	t.Helper()

	port, err := freePort()
	if err != nil {
		t.Fatalf("olrictest: %v", err)
	}

	c := config.New("local")
	c.BindAddr = "127.0.0.1"
	c.BindPort = port
	c.MemberlistConfig.BindAddr = "127.0.0.1"
	c.MemberlistConfig.BindPort = 0
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
