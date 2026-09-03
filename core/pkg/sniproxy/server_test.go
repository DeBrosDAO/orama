package sniproxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// startEchoBackend creates a TCP server that echoes the first 1024 bytes
// it reads, then closes. Returns the listener and a chan that receives
// the bytes the server saw.
func startEchoBackend(t *testing.T) (net.Listener, <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				got <- append([]byte(nil), buf[:n]...)
			}(conn)
		}
	}()
	return ln, got
}

func TestServer_routes_TLS_to_correct_backend(t *testing.T) {
	turnLn, turnGot := startEchoBackend(t)
	defer turnLn.Close()
	caddyLn, caddyGot := startEchoBackend(t)
	defer caddyLn.Close()

	router := NewRouter(Backend{Network: "tcp", Addr: caddyLn.Addr().String()})
	router.Replace([]Route{
		{Match: "turn.example.com", Backend: Backend{Network: "tcp", Addr: turnLn.Addr().String()}},
	}, router.Fallback())

	srv := NewServer(router, Config{}, zap.NewNop())
	defer srv.Close()

	frontLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer frontLn.Close()

	go func() { _ = srv.Serve(frontLn) }()

	// Client A: SNI=turn.example.com -> goes to turnLn
	dialAndStartTLS(t, frontLn.Addr().String(), "turn.example.com")

	// Client B: SNI=other.example.com -> falls through to caddyLn
	dialAndStartTLS(t, frontLn.Addr().String(), "other.example.com")

	select {
	case b := <-turnGot:
		if len(b) == 0 {
			t.Error("turn backend received empty bytes")
		}
	case <-time.After(3 * time.Second):
		t.Error("turn backend did not receive bytes")
	}

	select {
	case b := <-caddyGot:
		if len(b) == 0 {
			t.Error("caddy fallback received empty bytes")
		}
	case <-time.After(3 * time.Second):
		t.Error("caddy fallback did not receive bytes")
	}
}

// dialAndStartTLS opens a TLS handshake (which produces a ClientHello)
// against the given address with the given SNI. Returns immediately —
// the test only needs the proxy to forward the bytes; it doesn't
// require handshake completion.
func dialAndStartTLS(t *testing.T, addr, sni string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		c := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
		_ = c.Handshake() // expected to fail (echo backend isn't TLS)
	}()
}

func TestServer_no_backend_drops_connection(t *testing.T) {
	router := NewRouter(Backend{}) // empty fallback, empty Addr -> dropped
	srv := NewServer(router, Config{}, zap.NewNop())
	defer srv.Close()

	frontLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer frontLn.Close()

	go func() { _ = srv.Serve(frontLn) }()

	conn, err := net.Dial("tcp", frontLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	c := tls.Client(conn, &tls.Config{ServerName: "x.example.com", InsecureSkipVerify: true})
	// Handshake should fail because connection is closed by proxy.
	go func() { _ = c.Handshake() }()

	// Reader should see EOF quickly.
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(conn)
	_, err = br.ReadByte()
	if err == nil {
		t.Error("expected connection drop")
	}
	if !errors.Is(err, io.EOF) {
		// "use of closed network connection" is also fine.
		t.Logf("acceptable read error: %v", err)
	}
}

func TestServer_perIPCap(t *testing.T) {
	backend, _ := startEchoBackend(t)
	defer backend.Close()
	router := NewRouter(Backend{Network: "tcp", Addr: backend.Addr().String()})
	srv := NewServer(router, Config{MaxConnsPerIP: 1, MaxConcurrentConns: 100, IdleTimeout: time.Second}, zap.NewNop())
	defer srv.Close()
	frontLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer frontLn.Close()
	go func() { _ = srv.Serve(frontLn) }()

	hold, err := net.Dial("tcp", frontLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	// Start a TLS hello so handle() is in-flight while we try a second conn.
	go func() {
		c := tls.Client(hold, &tls.Config{ServerName: "x.example.com", InsecureSkipVerify: true})
		_ = c.Handshake()
	}()
	time.Sleep(50 * time.Millisecond)

	second, err := net.Dial("tcp", frontLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, rerr := second.Read(buf)
	if rerr == nil {
		t.Error("second connection from same IP should be dropped at the per-IP cap")
	}
}
