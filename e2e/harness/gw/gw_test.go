package gw

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// fakeGateway implements challenge and verify the way the gateway does:
// renders a SIWE message, recovers the signer, and checks a device signature
// with the gateway's own device-key code.
type fakeGateway struct {
	t         *testing.T
	lastProto int
	issued    map[string]bool
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.lastProto = r.ProtoMajor
	switch r.URL.Path {
	case PathChallenge:
		g.challenge(w, r)
	case PathVerify:
		g.verify(w, r)
	case "/ws":
		up := websocket.Upgrader{}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mt, msg, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(mt, msg)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no route","code":"NOT_FOUND"}`))
	}
}

func (g *fakeGateway) challenge(w http.ResponseWriter, r *http.Request) {
	var req ChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := &siw.Message{Chain: siw.Ethereum, Domain: r.Host, Address: req.Wallet, URI: "https://" + r.Host,
		ChainID: "1", Nonce: "nonce" + now.Format("150405") + "abcdef", IssuedAt: now, ExpirationTime: now.Add(time.Minute)}
	if req.DeviceID != "" {
		m.Resources = []string{"urn:orama:device:" + req.DeviceID}
	}
	text, err := m.Render()
	if err != nil {
		g.t.Errorf("render: %v", err)
	}
	g.issued[text] = true
	_ = json.NewEncoder(w).Encode(map[string]string{"message": text, "nonce": m.Nonce})
}

func (g *fakeGateway) verify(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !g.issued[req.Message] {
		http.Error(w, `{"error":"not issued"}`, http.StatusUnauthorized)
		return
	}
	delete(g.issued, req.Message)
	m, _ := siw.Parse(req.Message)
	addr, err := wallet.RecoverAddress(req.Message, req.Signature)
	if err != nil || addr != m.Address {
		http.Error(w, `{"error":"bad signature"}`, http.StatusUnauthorized)
		return
	}
	resp := map[string]any{"access_token": "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweCJ9.c2lnbmF0dXJlMQ", "refresh_token": "refresh-token-value-1", "subject": addr}
	if len(req.DeviceKey) > 0 {
		key, err := gwauth.ParseDeviceKey(req.DeviceKey)
		if err != nil || key.Verify([]byte(req.Message), req.DeviceSignature) != nil {
			http.Error(w, `{"error":"bad device"}`, http.StatusBadRequest)
			return
		}
		resp["device_id"] = key.ID()
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func startGateway(t *testing.T) (*Client, *fakeGateway, string) {
	t.Helper()
	g := &fakeGateway{t: t, issued: map[string]bool{}}
	srv := httptest.NewUnstartedServer(g)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(t.TempDir(), "evidence")
	rec, err := evidence.New(evDir, "gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(srv.URL, caFile, rec)
	if err != nil {
		t.Fatal(err)
	}
	return c.For(t), g, evDir
}

func TestSignIn_realSignatureAndHTTP11(t *testing.T) {
	c, g, evDir := startGateway(t)
	w, _ := wallet.NewEVM()
	s, err := c.SignIn(context.Background(), w, "e2e", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Subject != w.Address() || s.AccessToken == "" {
		t.Fatalf("session %+v", s)
	}
	if g.lastProto != 1 {
		t.Fatalf("request used HTTP/%d, want HTTP/1.1", g.lastProto)
	}
	recs, err := evidence.Load(evDir)
	if err != nil || len(recs) != 2 {
		t.Fatalf("evidence %v err %v", recs, err)
	}
	if strings.Contains(recs[1].Output, "refresh-token-value-1") {
		t.Fatal("refresh token reached evidence")
	}
}

func TestSignIn_withDevice(t *testing.T) {
	c, _, _ := startGateway(t)
	w, _ := wallet.NewEVM()
	for _, mk := range []func() (*wallet.Device, error){wallet.NewEd25519Device, wallet.NewES256Device} {
		d, _ := mk()
		s, err := c.SignIn(context.Background(), w, "e2e", d)
		if err != nil || s.DeviceID != d.ID() {
			t.Fatalf("%s: session %+v err %v", d.Alg(), s, err)
		}
	}
}

func TestNew_pinsOnlyTheRunCA(t *testing.T) {
	c, _, _ := startGateway(t)
	other := httptest.NewUnstartedServer(http.NotFoundHandler())
	other.TLS = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	other.StartTLS()
	defer other.Close()
	resp, err := c.WithBase(other.URL).Send(context.Background(), Req{Path: "/"})
	if err == nil {
		t.Fatalf("a server outside the pinned CA answered %d", resp.Status)
	}
	if _, err := New("https://x", filepath.Join(t.TempDir(), "none.pem"), nil); err == nil {
		t.Fatal("missing CA file accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	_ = os.WriteFile(empty, []byte("not pem"), 0o600)
	if _, err := New("https://x", empty, nil); err == nil {
		t.Fatal("CA file without certificates accepted")
	}
	if _, err := NewWithTLS("ftp://x", nil, nil); err == nil {
		t.Fatal("non-http URL accepted")
	}
}

func TestJSON_statusErrorAndCode(t *testing.T) {
	c, _, _ := startGateway(t)
	resp, err := c.JSON(context.Background(), http.MethodGet, "/nope", "tok", nil, nil)
	var se *StatusError
	if err == nil || !asStatus(err, &se) || se.Status != 404 {
		t.Fatalf("err %v", err)
	}
	if resp.ErrorCode() != "NOT_FOUND" {
		t.Fatalf("code %q", resp.ErrorCode())
	}
	if (&Response{Body: []byte("x")}).ErrorCode() != "" {
		t.Fatal("non-JSON body has a code")
	}
}

// selfSigned is a certificate for 127.0.0.1 from no CA the client trusts.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func asStatus(err error, target **StatusError) bool {
	se, ok := err.(*StatusError)
	*target = se
	return ok
}

func TestSend_duplicateHeadersAndRawBody(t *testing.T) {
	var seen http.Header
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
	}))
	defer srv.Close()
	c, err := NewWithTLS(srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Send(context.Background(), Req{Method: http.MethodPost, Path: "/x",
		Header: http.Header{"X-Api-Key": {"a", "b"}}, Body: []byte("{not json"), Bearer: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.Values("X-Api-Key")) != 2 || seen.Get("Authorization") != "Bearer t" || body != "{not json" {
		t.Fatalf("headers %v body %q", seen, body)
	}
}

func TestRaw_malformedRequest(t *testing.T) {
	c, _, _ := startGateway(t)
	out, err := c.Raw(context.Background(), []byte("GET /nope HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "HTTP/1.1 404") {
		t.Fatalf("raw response %q", out)
	}
}

func TestDialWS_echo(t *testing.T) {
	c, _, _ := startGateway(t)
	conn, resp, err := c.DialWS(context.Background(), "/ws", "tok", nil)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "hi" {
		t.Fatalf("echo %q err %v", msg, err)
	}
	if _, resp, err := c.DialWS(context.Background(), "/nope", "", nil); err == nil || resp.StatusCode != 404 {
		t.Fatal("refused upgrade reported as success")
	}
}
