// Package sshxtest is an in-process sshd for the harness's unit tests: it
// runs exec requests with sh -c on this machine, dials direct-tcpip channels
// (ssh -L) and serves tcpip-forward requests (ssh -R) on its own loopback,
// with the same key setup a run has (one client key, one pinned ed25519
// host key in a known_hosts file). It is never used against a fleet.
package sshxtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// Server is a running test sshd.
type Server struct {
	// Addr is "127.0.0.1:<port>"; KeyFile opens it as User; KnownHostsFile
	// pins its host key.
	Addr, User, KeyFile, KnownHostsFile string

	mu        sync.Mutex
	listeners map[uint32]net.Listener
}

// Start runs a server until the test ends.
func Start(t testing.TB) *Server {
	t.Helper()
	hostKey, _ := newSigner(t)
	client, clientPriv := newSigner(t)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(key.Marshal(), client.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sshxtest: listen: %v", err)
	}
	s := &Server{Addr: ln.Addr().String(), User: "root", listeners: map[uint32]net.Listener{}}
	t.Cleanup(func() { ln.Close(); s.closeForwards() })
	s.writeKeys(t, clientPriv, hostKey.PublicKey())
	go s.accept(ln, cfg)
	return s
}

// Target reaches the server the way the harness reaches a node.
func (s *Server) Target() sshx.Target {
	return sshx.Target{Host: s.Addr, User: s.User, KeyFile: s.KeyFile, KnownHostsFile: s.KnownHostsFile}
}

func newSigner(t testing.TB) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sshxtest: generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sshxtest: signer: %v", err)
	}
	return signer, priv
}

func (s *Server) writeKeys(t testing.TB, client ed25519.PrivateKey, host ssh.PublicKey) {
	t.Helper()
	dir := t.TempDir()
	block, err := ssh.MarshalPrivateKey(client, "")
	if err != nil {
		t.Fatalf("sshxtest: marshal key: %v", err)
	}
	s.KeyFile = filepath.Join(dir, "id")
	if err := os.WriteFile(s.KeyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("sshxtest: write key: %v", err)
	}
	s.KnownHostsFile = filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(s.Addr)}, host) + "\n"
	if err := os.WriteFile(s.KnownHostsFile, []byte(line), 0o600); err != nil {
		t.Fatalf("sshxtest: write known_hosts: %v", err)
	}
}

func (s *Server) accept(ln net.Listener, cfg *ssh.ServerConfig) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn, cfg)
	}
}

func (s *Server) serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	go s.globalRequests(sc, reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			go serveSession(nc)
		case "direct-tcpip":
			go serveDirect(nc)
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "session and direct-tcpip only")
		}
	}
}

func (s *Server) closeForwards() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for port, ln := range s.listeners {
		ln.Close()
		delete(s.listeners, port)
	}
}
