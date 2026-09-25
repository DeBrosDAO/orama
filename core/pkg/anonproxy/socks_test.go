package anonproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// socksRequest is what the fake SOCKS5 server saw for one CONNECT.
type socksRequest struct {
	user string
	dest string
}

// fakeSOCKS5 is a minimal SOCKS5 server (RFC 1928 + RFC 1929 user/pass) that
// records each CONNECT and then answers the tunnelled stream as a tiny HTTP
// server, so tests can see exactly what a client sent to the proxy.
func fakeSOCKS5(t *testing.T) (addr string, requests <-chan socksRequest) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ch := make(chan socksRequest, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSOCKS(conn, ch)
		}
	}()
	return ln.Addr().String(), ch
}

func serveSOCKS(conn net.Conn, ch chan<- socksRequest) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	var req socksRequest
	if containsByte(methods, 0x02) {
		_, _ = conn.Write([]byte{0x05, 0x02})
		user, ok := readUserPass(r)
		if !ok {
			return
		}
		req.user = user
		_, _ = conn.Write([]byte{0x01, 0x00})
	} else {
		_, _ = conn.Write([]byte{0x05, 0x00})
	}
	dest, ok := readConnect(r)
	if !ok {
		return
	}
	req.dest = dest
	ch <- req
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	if _, err := http.ReadRequest(r); err != nil {
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
}

func containsByte(b []byte, want byte) bool {
	for _, c := range b {
		if c == want {
			return true
		}
	}
	return false
}

func readUserPass(r *bufio.Reader) (string, bool) {
	ver, err := r.ReadByte()
	if err != nil || ver != 0x01 {
		return "", false
	}
	user, ok := readLenPrefixed(r)
	if !ok {
		return "", false
	}
	if _, ok := readLenPrefixed(r); !ok {
		return "", false
	}
	return user, true
}

func readLenPrefixed(r *bufio.Reader) (string, bool) {
	n, err := r.ReadByte()
	if err != nil {
		return "", false
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", false
	}
	return string(b), true
}

func readConnect(r *bufio.Reader) (string, bool) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return "", false
	}
	var host string
	switch hdr[3] {
	case 0x01:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, ip); err != nil {
			return "", false
		}
		host = net.IP(ip).String()
	case 0x03:
		name, ok := readLenPrefixed(r)
		if !ok {
			return "", false
		}
		host = name
	case 0x04:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, ip); err != nil {
			return "", false
		}
		host = net.IP(ip).String()
	default:
		return "", false
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(r, port); err != nil {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), true
}

func nextRequest(t *testing.T, ch <-chan socksRequest) socksRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("the SOCKS proxy never received a CONNECT")
		return socksRequest{}
	}
}

func TestAddress_isTheSharedTorSOCKSConstant(t *testing.T) {
	if got, want := Address(), constants.TorSOCKSAddr(); got != want {
		t.Errorf("Address() = %q, want %q", got, want)
	}
	if constants.TorSOCKSAddr() != "127.0.0.1:9050" {
		t.Errorf("TorSOCKSAddr() = %q, want loopback 127.0.0.1:9050", constants.TorSOCKSAddr())
	}
}

// The old client dialled loopback and private addresses directly, so a
// redirect to 10.0.0.x reached the WireGuard overlay from a request that was
// meant to leave through the anonymity network. Every destination must go to
// the proxy, where Tor refuses internal addresses.
func TestNewHTTPClient_sendsPrivateDestinationsThroughSOCKS(t *testing.T) {
	addr, reqs := fakeSOCKS5(t)
	client := newHTTPClient(addr)
	resp, err := client.Get("http://10.0.0.5:8080/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if got := nextRequest(t, reqs).dest; got != "10.0.0.5:8080" {
		t.Errorf("proxy saw CONNECT %q, want 10.0.0.5:8080", got)
	}
}

// A host name must reach the proxy unresolved so the Tor exit does the DNS.
func TestNewHTTPClient_passesHostnamesUnresolved(t *testing.T) {
	addr, reqs := fakeSOCKS5(t)
	client := newHTTPClient(addr)
	resp, err := client.Get("http://rpc.example.invalid/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if got := nextRequest(t, reqs).dest; got != "rpc.example.invalid:80" {
		t.Errorf("proxy saw CONNECT %q, want the unresolved name rpc.example.invalid:80", got)
	}
}

func TestNewHTTPClient_proxyDownIsAnErrorNotADirectDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()

	target := httpTarget(t)
	if _, err := newHTTPClient(dead).Get(target); err == nil {
		t.Fatal("with the SOCKS port closed the request must fail, not go direct")
	}
}

// httpTarget serves on loopback so a direct dial would succeed — the test
// above passes only because nothing bypasses the proxy.
func httpTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "direct")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String() + "/"
}

func TestDialThrough_isolationKeyIsTheSOCKSUsername(t *testing.T) {
	addr, reqs := fakeSOCKS5(t)
	conn, err := dialThrough(context.Background(), addr, "example.org:443", "user-circuit-1")
	if err != nil {
		t.Fatalf("dialThrough: %v", err)
	}
	_ = conn.Close()
	req := nextRequest(t, reqs)
	if req.user != "user-circuit-1" {
		t.Errorf("SOCKS username = %q, want the isolation key", req.user)
	}
	if req.dest != "example.org:443" {
		t.Errorf("CONNECT = %q, want example.org:443", req.dest)
	}
}

func TestDialThrough_emptyKeySendsNoCredentials(t *testing.T) {
	addr, reqs := fakeSOCKS5(t)
	conn, err := dialThrough(context.Background(), addr, "example.org:443", "")
	if err != nil {
		t.Fatalf("dialThrough: %v", err)
	}
	_ = conn.Close()
	if req := nextRequest(t, reqs); req.user != "" {
		t.Errorf("SOCKS username = %q, want none", req.user)
	}
}

func TestDialThrough_expiredContext(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := dialThrough(ctx, "127.0.0.1:1", "example.org:443", "k")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestSocksReachable(t *testing.T) {
	addr, _ := fakeSOCKS5(t)
	if !socksReachable(addr) {
		t.Error("an accepting SOCKS port must be reported reachable")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()
	if socksReachable(dead) {
		t.Error("a closed port must be reported unreachable")
	}
}
