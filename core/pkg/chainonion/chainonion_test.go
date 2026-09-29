package chainonion_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/chainonion"
)

const testOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

func TestBase(t *testing.T) {
	cases := map[string]struct {
		in, want string
		bad      bool
	}{
		"default port":  {in: testOnion, want: "http://" + testOnion + ":80"},
		"explicit port": {in: testOnion + ":31003", want: "http://" + testOnion + ":31003"},
		"upper case":    {in: strings.ToUpper(testOnion), want: "http://" + testOnion + ":80"},
		"clearnet host": {in: "chain.example.com", bad: true},
		"ip":            {in: "127.0.0.1:31003", bad: true},
		"v2 length":     {in: "abcdefghijklmnop.onion", bad: true},
		"subdomain":     {in: "www." + testOnion, bad: true},
		"scheme":        {in: "http://" + testOnion, bad: true},
		"empty":         {in: "", bad: true},
		"empty port":    {in: testOnion + ":", bad: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := chainonion.Base(tc.in)
			if tc.bad {
				if !errors.Is(err, chainonion.ErrNotOnion) {
					t.Fatalf("err = %v, want ErrNotOnion", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Base(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// connect is one CONNECT the fake proxy saw.
type connect struct{ target, user string }

// fakeSOCKS is a SOCKS5 proxy that records each CONNECT and its credentials.
// It tunnels to upstream, or refuses every connection when upstream is empty.
type fakeSOCKS struct {
	ln       net.Listener
	upstream string
	mu       sync.Mutex
	seen     []connect
}

func newFakeSOCKS(t *testing.T, upstream string) *fakeSOCKS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSOCKS{ln: ln, upstream: upstream}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSOCKS) addr() string { return f.ln.Addr().String() }

func (f *fakeSOCKS) connects() []connect {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]connect(nil), f.seen...)
}

func (f *fakeSOCKS) serve(c net.Conn) {
	defer c.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	user := ""
	if strings.Contains(string(methods), "\x02") {
		c.Write([]byte{5, 2})
		var ulen [2]byte
		if _, err := io.ReadFull(c, ulen[:]); err != nil {
			return
		}
		u := make([]byte, ulen[1])
		io.ReadFull(c, u)
		var plen [1]byte
		io.ReadFull(c, plen[:])
		io.ReadFull(c, make([]byte, plen[0]))
		user = string(u)
		c.Write([]byte{1, 0})
	} else {
		c.Write([]byte{5, 0})
	}
	req := make([]byte, 5)
	if _, err := io.ReadFull(c, req); err != nil || req[3] != 3 {
		return
	}
	rest := make([]byte, int(req[4])+2)
	if _, err := io.ReadFull(c, rest); err != nil {
		return
	}
	host := string(rest[:req[4]])
	port := int(rest[req[4]])<<8 | int(rest[req[4]+1])
	f.mu.Lock()
	f.seen = append(f.seen, connect{target: net.JoinHostPort(host, strconv.Itoa(port)), user: user})
	f.mu.Unlock()

	if f.upstream == "" {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	up, err := net.Dial("tcp", f.upstream)
	if err != nil {
		c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

// trapClearnet makes any use of the default HTTP transport, and any TCP
// connection to a decoy listener, fail the test.
func trapClearnet(t *testing.T) (decoy string) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("clearnet request through the default transport: %s", r.URL)
		return nil, errors.New("trapped")
	})
	t.Cleanup(func() { http.DefaultTransport = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hit atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hit.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		if n := hit.Load(); n != 0 {
			t.Errorf("%d direct connection(s) reached the clearnet decoy", n)
		}
	})
	return ln.Addr().String()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func chainServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestClient_requestsGoThroughTheProxyToTheOnionName(t *testing.T) {
	trapClearnet(t)
	up := chainServer(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok "+r.URL.Path) })
	proxy := newFakeSOCKS(t, up)

	client, err := chainonion.NewClient(proxy.addr())
	if err != nil {
		t.Fatal(err)
	}
	base, err := chainonion.Base(testOnion)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(base + "/cosmos/tx/v1beta1/txs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok /cosmos/tx/v1beta1/txs" {
		t.Fatalf("body = %q", body)
	}
	seen := proxy.connects()
	if len(seen) != 1 || seen[0].target != testOnion+":80" {
		t.Fatalf("proxy saw %+v, want one CONNECT to the unresolved onion name", seen)
	}
	if seen[0].user == "" {
		t.Fatal("no isolation credential was sent")
	}
}

func TestClient_eachClientGetsItsOwnCircuit(t *testing.T) {
	up := chainServer(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	proxy := newFakeSOCKS(t, up)
	base, _ := chainonion.Base(testOnion)

	get := func(c *http.Client) {
		t.Helper()
		resp, err := c.Get(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	first, _ := chainonion.NewClient(proxy.addr())
	second, _ := chainonion.NewClient(proxy.addr())
	get(first)
	get(second)
	seen := proxy.connects()
	if len(seen) != 2 {
		t.Fatalf("proxy saw %d connections, want 2", len(seen))
	}
	if seen[0].user == seen[1].user {
		t.Fatalf("two transactions shared the isolation credential %q", seen[0].user)
	}
}

func TestClient_oneClientKeepsOneCircuitForItsTransaction(t *testing.T) {
	up := chainServer(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	proxy := newFakeSOCKS(t, up)
	base, _ := chainonion.Base(testOnion)
	c, _ := chainonion.NewClient(proxy.addr())
	for _, path := range []string{"/account", "/broadcast"} {
		resp, err := c.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Force a new connection so the credential is sent again.
		c.CloseIdleConnections()
	}
	seen := proxy.connects()
	if len(seen) != 2 || seen[0].user != seen[1].user {
		t.Fatalf("account read and broadcast used different circuits: %+v", seen)
	}
}

func TestClient_proxyRefusalIsAnErrorAndNothingGoesToTheClearnet(t *testing.T) {
	decoy := trapClearnet(t)
	t.Setenv("HTTP_PROXY", "http://"+decoy)
	t.Setenv("http_proxy", "http://"+decoy)
	proxy := newFakeSOCKS(t, "")
	client, _ := chainonion.NewClient(proxy.addr())
	base, _ := chainonion.Base(testOnion)

	_, err := client.Get(base + "/cosmos/tx/v1beta1/txs")
	if !errors.Is(err, chainonion.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "nothing was tried outside Tor") {
		t.Fatalf("the error does not say the clearnet was not used: %v", err)
	}
	if n := len(proxy.connects()); n != 1 {
		t.Fatalf("proxy saw %d connections, want exactly the one refused attempt", n)
	}
}

func TestClient_proxyDownIsAnErrorAndNothingGoesToTheClearnet(t *testing.T) {
	trapClearnet(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()

	client, _ := chainonion.NewClient(dead)
	base, _ := chainonion.Base(testOnion)
	start := time.Now()
	_, err = client.Get(base + "/")
	if !errors.Is(err, chainonion.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a closed proxy port took the whole request timeout to fail")
	}
}

func TestClient_refusesRedirectsOffTheOnionService(t *testing.T) {
	trapClearnet(t)
	up := chainServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://chain.example.com/steal", http.StatusFound)
	})
	proxy := newFakeSOCKS(t, up)
	client, _ := chainonion.NewClient(proxy.addr())
	base, _ := chainonion.Base(testOnion)

	_, err := client.Get(base + "/")
	if err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("err = %v, want a refused redirect", err)
	}
	if n := len(proxy.connects()); n != 1 {
		t.Fatalf("proxy saw %d connections, want 1", n)
	}
}
