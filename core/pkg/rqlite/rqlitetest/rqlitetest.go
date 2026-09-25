// Package rqlitetest runs a real single-node rqlited for tests.
//
// rqlite is not SQLite: errors, types and consistency travel over its HTTP API
// and through gorqlite, and behaviour that passes against sqlite3 can fail in
// production. Tests that depend on what rqlite actually returns use this.
// When rqlited is not installed the test is skipped, with the reason stated.
package rqlitetest

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/rqlite/gorqlite/stdlib"
)

// readyTimeout bounds how long a test waits for the node to elect itself.
const readyTimeout = 15 * time.Second

// Start runs rqlited in a temp dir and returns a client wired like a gateway's
// (database/sql plus the native connection). The node stops when the test ends.
func Start(t *testing.T) rqlite.Client {
	t.Helper()

	bin, err := exec.LookPath("rqlited")
	if err != nil {
		t.Skip("rqlited not on PATH; install rqlite to run tests against a real node")
	}
	httpPort, raftPort := freePort(t), freePort(t)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)

	cmd := exec.Command(bin,
		"-http-addr", httpAddr,
		"-raft-addr", fmt.Sprintf("127.0.0.1:%d", raftPort),
		t.TempDir())
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("rqlitetest: failed to start rqlited: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if err := waitLeader(httpAddr); err != nil {
		t.Fatalf("rqlitetest: %v\nrqlited stderr:\n%s", err, stderr.String())
	}

	// The same parameters the gateway adds (appendRQLiteQueryParams).
	dsn := "http://" + httpAddr + "?disableClusterDiscovery=true&level=weak"
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		t.Fatalf("rqlitetest: sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	client, err := rqlite.NewClientWithDSN(db, dsn)
	if err != nil {
		t.Fatalf("rqlitetest: NewClientWithDSN: %v", err)
	}
	return client
}

func waitLeader(httpAddr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+httpAddr+"/readyz", nil)
		if err != nil {
			return fmt.Errorf("build readyz request: %w", err)
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "leader ok") {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("rqlited at %s had no leader within %s", httpAddr, readyTimeout)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("rqlitetest: failed to reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("rqlitetest: failed to release port %d: %v", port, err)
	}
	return port
}
