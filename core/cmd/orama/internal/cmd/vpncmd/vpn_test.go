package vpncmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/onionnet"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/DeBrosOfficial/network/pkg/txgate"
)

const (
	testOnion   = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	otherOnion  = "bbcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	bootedAndUp = "echo 'Bootstrapped 100% (done): Done'\nwhile true; do sleep 0.1; done"
)

func networkFile(t *testing.T, private bool, onions ...string) string {
	t.Helper()
	n := tornet.Network{Name: "stagenet", Private: private, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300, ValidatorOnions: onions}
	for i, ip := range []string{"57.129.166.16", "57.129.166.17", "161.97.184.199"} {
		n.Authorities = append(n.Authorities, tornet.Authority{
			Nickname: fmt.Sprintf("OramaAuth%d", i+1), Address: ip, ORPort: 31020, DirPort: 31021,
			V3Ident: fmt.Sprintf("%040X", 0xA0+i), Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		})
	}
	body, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeTor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntrap 'exit 0' INT TERM\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	t.Setenv(NetworkEnv, "")
	var out bytes.Buffer
	cmd := New()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func dataDir(t *testing.T) string { return filepath.Join(t.TempDir(), "tor-data") }

func TestUp_aTorThatStopsClosesTheProxyAndFailsLoudly(t *testing.T) {
	tor := fakeTor(t, "echo 'Bootstrapped 100%'\nsleep 0.3\nexit 4")
	_, err := run(t, context.Background(), "up", "--network", networkFile(t, true), "--tor", tor, "--data-dir", dataDir(t), "--socks", "127.0.0.1:19150")
	if err == nil || !strings.Contains(err.Error(), "is closed and traffic is not routed around it") {
		t.Fatalf("err = %v", err)
	}
	if clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Errorf("code = %d", clierr.CodeOf(err))
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestUp_stopsTorWhenInterrupted(t *testing.T) {
	t.Setenv(NetworkEnv, "")
	mark := filepath.Join(t.TempDir(), "stopped")
	tor := fakeTor(t, "trap 'echo stopped > "+mark+"; exit 0' TERM\n"+bootedAndUp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuf
	cmd := New()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"up", "--network", networkFile(t, true), "--tor", tor, "--data-dir", dataDir(t), "--socks", "127.0.0.1:19151", "--dns", "127.0.0.1:19153"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	for deadline := time.Now().Add(20 * time.Second); !strings.Contains(out.String(), "DNS resolver"); {
		if time.Now().After(deadline) {
			t.Fatalf("up never announced the proxy:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("interrupt is a clean exit, got %v", err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("tor was not told to stop: %v", err)
	}
	for _, want := range []string{"Joined the stagenet Tor network", "SOCKS5 proxy: 127.0.0.1:19151", "DNS resolver: 127.0.0.1:19153"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestUp_refusals(t *testing.T) {
	good := networkFile(t, true)
	tor := fakeTor(t, bootedAndUp)
	cases := map[string][]string{
		"no network":                    {"up", "--tor", tor},
		"a public network":              {"up", "--network", networkFile(t, false), "--tor", tor},
		"a network file that is absent": {"up", "--network", filepath.Join(t.TempDir(), "absent.json"), "--tor", tor},
		"a proxy open to the network":   {"up", "--network", good, "--tor", tor, "--socks", "0.0.0.0:9150"},
		"a resolver on another host":    {"up", "--network", good, "--tor", tor, "--dns", "10.1.1.1:53"},
	}
	for name, args := range cases {
		args = append(args, "--data-dir", dataDir(t))
		if _, err := run(t, context.Background(), args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_, err := run(t, context.Background(), "up", "--network", networkFile(t, false), "--tor", tor)
	if err == nil || !strings.Contains(err.Error(), "not launched") || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("public network: %v", err)
	}
	if _, err := run(t, context.Background(), "up", "--network", good, "--tor", "/nonexistent/tor", "--data-dir", dataDir(t)); err == nil || !strings.Contains(err.Error(), "start tor") {
		t.Errorf("missing tor binary: %v", err)
	}
}

func TestUp_networkFromTheEnvironment(t *testing.T) {
	tor := fakeTor(t, "echo 'Bootstrapped 100%'\nexit 0")
	cmd := New()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	t.Setenv(NetworkEnv, networkFile(t, true))
	cmd.SetArgs([]string{"up", "--tor", tor, "--data-dir", dataDir(t), "--socks", "127.0.0.1:19152"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tor stopped") {
		t.Fatalf("the variable must supply the network, got %v", err)
	}
}

func TestCheck_needsSomewhereToLook(t *testing.T) {
	_, err := run(t, context.Background(), "check", "--network", networkFile(t, true), "--tor", fakeTor(t, bootedAndUp), "--data-dir", dataDir(t))
	if err == nil || !strings.Contains(err.Error(), "validator onion") || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v", err)
	}
	_, err = run(t, context.Background(), "check", "--network", networkFile(t, true), "--onion", "chain.example.com", "--tor", fakeTor(t, bootedAndUp), "--data-dir", dataDir(t))
	if err == nil || !strings.Contains(err.Error(), "onion") {
		t.Fatalf("a clearnet host as the onion: %v", err)
	}
}

func TestCheck_aNetworkThatCannotBeJoinedFails(t *testing.T) {
	tor := fakeTor(t, "echo '[warn] no authority answered'\nexit 3")
	_, err := run(t, context.Background(), "check", "--network", networkFile(t, true, testOnion), "--tor", tor, "--data-dir", dataDir(t))
	if err == nil || !strings.Contains(err.Error(), "no authority answered") || clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Fatalf("err = %v", err)
	}
}

// socksServer is a SOCKS5 proxy that records the target of each CONNECT and
// the credential the client authenticated with, then answers the HTTP request
// that follows with handler.
type socksServer struct {
	addr    string
	mu      sync.Mutex
	hosts   []string
	atyps   []byte
	users   []string
	handler http.Handler
}

// newSOCKSServer answers every request with status and an empty JSON object.
func newSOCKSServer(t *testing.T, status int) *socksServer {
	t.Helper()
	return newSOCKSServerFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "{}")
	}))
}

func newSOCKSServerFor(t *testing.T, handler http.Handler) *socksServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksServer{addr: ln.Addr().String(), handler: handler}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *socksServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return
	}
	if _, err := io.ReadFull(r, make([]byte, hdr[1])); err != nil {
		return
	}
	c.Write([]byte{5, 2})
	ver, _ := r.ReadByte()
	_ = ver
	ulen, _ := r.ReadByte()
	user := make([]byte, ulen)
	io.ReadFull(r, user)
	plen, _ := r.ReadByte()
	io.ReadFull(r, make([]byte, plen))
	c.Write([]byte{1, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil {
		return
	}
	var host string
	if req[3] == 3 {
		n, _ := r.ReadByte()
		b := make([]byte, n)
		io.ReadFull(r, b)
		host = string(b)
	} else {
		io.ReadFull(r, make([]byte, 4))
	}
	io.ReadFull(r, make([]byte, 2))
	s.mu.Lock()
	s.hosts, s.atyps, s.users = append(s.hosts, host), append(s.atyps, req[3]), append(s.users, string(user))
	s.mu.Unlock()
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 0)
	c.Write(append([]byte{5, 0, 0, 1, 0, 0, 0, 0}, port...))
	httpReq, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httpReq)
	resp := rec.Result()
	resp.ProtoMajor, resp.ProtoMinor = 1, 1
	resp.ContentLength = int64(rec.Body.Len())
	resp.Write(c)
}

func TestProbe_namesTheOnionToTheProxyUnresolvedOnACircuitOfItsOwn(t *testing.T) {
	srv := newSOCKSServer(t, http.StatusOK)
	tor := &onionnet.Tor{SocksAddr: srv.addr}
	for i := 0; i < 2; i++ {
		if err := probe(context.Background(), tor, testOnion+":26657"); err != nil {
			t.Fatal(err)
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.hosts) != 2 || srv.hosts[0] != testOnion || srv.atyps[0] != 3 {
		t.Fatalf("the proxy saw hosts %v atyps %v: the name must reach it unresolved", srv.hosts, srv.atyps)
	}
	if srv.users[0] == "" || srv.users[0] == srv.users[1] {
		t.Errorf("circuit isolation credentials %q and %q must be set and differ", srv.users[0], srv.users[1])
	}
}

func TestProbe_failures(t *testing.T) {
	bad := newSOCKSServer(t, http.StatusServiceUnavailable)
	if err := probe(context.Background(), &onionnet.Tor{SocksAddr: bad.addr}, testOnion); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("a non-200 answer: %v", err)
	}
	dead, _ := onionnet.FreeLoopbackAddr()
	if err := probe(context.Background(), &onionnet.Tor{SocksAddr: dead}, testOnion); err == nil || !strings.Contains(err.Error(), "nothing was tried outside Tor") {
		t.Errorf("a dead proxy must not mean a direct connection: %v", err)
	}
	if err := probe(context.Background(), &onionnet.Tor{SocksAddr: dead}, "chain.example.com"); err == nil {
		t.Error("a clearnet host was probed")
	}
}

func TestProbeAll_needsOneAnswerAndSaysWhichFailed(t *testing.T) {
	good := newSOCKSServer(t, http.StatusOK)
	var out bytes.Buffer
	cmd := New()
	cmd.SetOut(&out)
	if err := probeAll(context.Background(), cmd, &onionnet.Tor{SocksAddr: good.addr}, "stagenet", []string{testOnion, otherOnion}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "2 of 2 validator onion services reached") {
		t.Errorf("output:\n%s", out.String())
	}

	bad := newSOCKSServer(t, http.StatusBadGateway)
	out.Reset()
	err := probeAll(context.Background(), cmd, &onionnet.Tor{SocksAddr: bad.addr}, "stagenet", []string{testOnion})
	if err == nil || clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(out.String(), "FAIL  "+testOnion) {
		t.Errorf("err = %v, output:\n%s", err, out.String())
	}
}

// chainAPI stands in for a validator's chain REST API: the account read of an
// address with no account answers the chain's own "not found", and it records
// every path it was asked.
func chainAPI(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":5,"message":"rpc error: code = NotFound desc = account not found","details":[]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func realGate(t *testing.T, upstream string) *txgate.Gate {
	t.Helper()
	g, err := txgate.New(txgate.Config{Upstream: upstream, Rate: 100, Burst: 100, InFlight: 4, UpstreamTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The check must only ask what the gate serves: the account read passes
// through the real gate to the chain API, and /status, which the old probe
// used, is refused by the gate.
func TestProbe_usesOnlyWhatTheTxGateServes(t *testing.T) {
	api, asked := chainAPI(t)
	srv := newSOCKSServerFor(t, realGate(t, api.URL))
	if err := probe(context.Background(), &onionnet.Tor{SocksAddr: srv.addr}, testOnion); err != nil {
		t.Fatalf("the account route through the gate: %v", err)
	}
	if got := asked(); len(got) != 1 || got[0] != probePath {
		t.Fatalf("the chain API was asked %v, want only %s", got, probePath)
	}
	rec := httptest.NewRecorder()
	realGate(t, api.URL).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the gate answered /status with %d; the contract is that it refuses it", rec.Code)
	}
	if err := checkAnswer(rec.Code, rec.Body.Bytes()); err == nil {
		t.Fatal("the gate's own refusal passed the check")
	}
}

func TestProbe_aGateThatCannotReachTheChainFails(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	srv := newSOCKSServerFor(t, realGate(t, dead.URL))
	if err := probe(context.Background(), &onionnet.Tor{SocksAddr: srv.addr}, testOnion); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("a gate with no chain behind it: %v", err)
	}
}

func TestCheckAnswer(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		ok     bool
	}{
		"the account exists":            {200, `{"account":{}}`, true},
		"the chain's account not found": {404, `{"code":5,"message":"not found","details":[]}`, true},
		"the gate's refusal":            {404, `{"error":"not served over the onion service"}`, false},
		"a 404 that is not JSON":        {404, `<html>`, false},
		"the gate is busy":              {429, `{"error":"busy, try another validator"}`, false},
		"the chain is down":             {502, `{"error":"the chain API did not answer"}`, false},
		"a bad request":                 {400, `{"code":3,"message":"decoding bech32 failed"}`, false},
		"an empty body":                 {404, ``, false},
	}
	for name, c := range cases {
		if err := checkAnswer(c.status, []byte(c.body)); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%t", name, err, c.ok)
		}
	}
}
