package sniproxy

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// dialAndPeek dials a TLS handshake to the given listener and returns
// what PeekClientHello on the server side parsed.
func dialAndPeek(t *testing.T, ln net.Listener, sni string) (string, []byte, error) {
	t.Helper()

	type result struct {
		sni    string
		peeked []byte
		err    error
	}
	resCh := make(chan result, 1)

	// Server side: accept once, peek SNI.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer conn.Close()
		s, p, err := PeekClientHello(conn, 2*time.Second)
		resCh <- result{sni: s, peeked: p, err: err}
	}()

	// Client side: kick off a TLS handshake. We don't care if it
	// completes (no server cert) — we only need ClientHello to be sent.
	// Use a goroutine so the test doesn't deadlock waiting on Handshake.
	go func() {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		c := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		})
		_ = c.Handshake() // expected to fail; we only needed the ClientHello
	}()

	select {
	case r := <-resCh:
		return r.sni, r.peeked, r.err
	case <-time.After(5 * time.Second):
		return "", nil, errors.New("test timeout")
	}
}

func TestPeekClientHello_returns_sni(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	sni, peeked, err := dialAndPeek(t, ln, "example.com")
	if err != nil {
		t.Fatalf("PeekClientHello: %v", err)
	}
	if sni != "example.com" {
		t.Errorf("expected sni=example.com, got %q", sni)
	}
	if len(peeked) == 0 {
		t.Error("expected non-empty peeked bytes")
	}
}

func TestPeekClientHello_lowercases_sni(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	sni, _, err := dialAndPeek(t, ln, "Example.COM")
	if err != nil {
		t.Fatal(err)
	}
	if sni != "example.com" {
		t.Errorf("expected lowercase, got %q", sni)
	}
}

func TestPeekClientHello_non_tls_returns_error(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		// Send something that isn't a TLS handshake record.
		_, _ = a.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
		_ = a.Close()
	}()

	_, _, err := PeekClientHello(b, 1*time.Second)
	if err == nil {
		t.Fatal("expected error for non-TLS bytes")
	}
}

func TestPeekClientHello_short_record_returns_error(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		// One byte, then close — too short for record header.
		_, _ = a.Write([]byte{22})
		_ = a.Close()
	}()

	_, _, err := PeekClientHello(b, 1*time.Second)
	if err == nil {
		t.Fatal("expected error for short record")
	}
	// EOF or read error is acceptable.
	if !errors.Is(err, io.EOF) && err.Error() == "" {
		t.Logf("error: %v", err)
	}
}

func TestPeekClientHello_concurrent_safe(t *testing.T) {
	// Verify no shared state leaks between PeekClientHello calls.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			c := tls.Client(conn, &tls.Config{ServerName: "x.example.com", InsecureSkipVerify: true})
			_ = c.Handshake()
		}()
	}
	for i := 0; i < 4; i++ {
		conn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		sni, _, err := PeekClientHello(conn, 2*time.Second)
		conn.Close()
		if err != nil {
			t.Errorf("peek %d: %v", i, err)
		}
		if sni != "x.example.com" {
			t.Errorf("peek %d: got %q", i, sni)
		}
	}
	wg.Wait()
}
