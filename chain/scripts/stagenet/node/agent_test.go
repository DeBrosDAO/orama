package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

func testAccount(t *testing.T) tx.Account {
	t.Helper()
	acct, err := tx.DeriveAccount(secp256k1.GenPrivKey().Bytes())
	require.NoError(t, err)
	return acct
}

func postSign(t *testing.T, h http.Handler, body string) (*httptest.ResponseRecorder, agentEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, signPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env struct {
		OK    bool     `json:"ok"`
		Data  signData `json:"data"`
		Error string   `json:"error"`
		Code  string   `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return rec, agentEnvelope{OK: env.OK, Data: env.Data, Error: env.Error, Code: env.Code}
}

func TestAgentSign_signatureVerifiesOverTheSignDocForTheAccountKey(t *testing.T) {
	acct := testAccount(t)
	doc := []byte("a sign document")
	body, err := json.Marshal(map[string]string{"signDoc": base64.StdEncoding.EncodeToString(doc)})
	require.NoError(t, err)

	rec, env := postSign(t, newAgentHandler(acct), string(body))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, env.OK)
	data := env.Data.(signData)
	require.Equal(t, acct.Address, data.Address)

	sig, err := base64.StdEncoding.DecodeString(data.Signature)
	require.NoError(t, err)
	pub, err := base64.StdEncoding.DecodeString(data.PubKey)
	require.NoError(t, err)
	require.Len(t, sig, 64)
	require.Len(t, pub, 33, "the CLI checks for a 33-byte compressed key")
	require.Equal(t, acct.PublicKey().Bytes(), pub)
	require.True(t, acct.PublicKey().VerifySignature(doc, sig), "the signature is over the sign doc, as the SDK signs it")
	require.False(t, acct.PublicKey().VerifySignature([]byte("another doc"), sig))
}

func TestAgentSign_refusesABadRequest(t *testing.T) {
	h := newAgentHandler(testAccount(t))
	for name, body := range map[string]string{
		"not json":        "{",
		"missing signDoc": `{}`,
		"not base64":      `{"signDoc":"!!!"}`,
		"empty doc":       `{"signDoc":""}`,
	} {
		rec, env := postSign(t, h, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
		require.False(t, env.OK, name)
		require.Equal(t, "INVALID_REQUEST", env.Code, name)
	}
}

func TestAgentAccount_returnsTheAddressAndKeyButNoSignature(t *testing.T) {
	acct := testAccount(t)
	rec := httptest.NewRecorder()
	newAgentHandler(acct).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accountPath, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data signData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, acct.Address, env.Data.Address)
	require.Empty(t, env.Data.Signature)
	require.NotContains(t, rec.Body.String(), "priv")
}

func TestParseListen(t *testing.T) {
	spec, err := parseListen("/run/orama-stagenet/agent.sock:1000")
	require.NoError(t, err)
	require.Equal(t, listenSpec{Path: "/run/orama-stagenet/agent.sock", UID: 1000}, spec)

	for _, bad := range []string{"", "/x.sock", "/x.sock:", ":5", "/x.sock:abc", "/x.sock:-1", "x.sock:1"} {
		_, err := parseListen(bad)
		require.Error(t, err, bad)
	}
}

// shortTempDir is a temp directory whose path fits a unix socket path (104 bytes on darwin).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sn")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestServeAgent_servesOverAUnixSocketAndRemovesItOnStop(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "sub", "agent.sock")
	acct := testAccount(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveAgent(ctx, newAgentHandler(acct), []listenSpec{{Path: sock, UID: os.Getuid()}}) }()

	require.Eventually(t, func() bool { _, err := os.Lstat(sock); return err == nil }, 5*time.Second, 20*time.Millisecond)
	fi, err := os.Lstat(sock)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	resp, err := client.Post("http://agent"+signPath, "application/json",
		bytes.NewReader([]byte(`{"signDoc":"`+base64.StdEncoding.EncodeToString([]byte("x"))+`"}`)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	require.NoError(t, <-done)
	_, err = os.Lstat(sock)
	require.True(t, os.IsNotExist(err), "the socket is removed when the agent stops")
}

func TestOpenSocket_replacesAStaleSocketButNotARegularFile(t *testing.T) {
	dir := shortTempDir(t)
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	again, err := openSocket(listenSpec{Path: stale, UID: os.Getuid()})
	require.NoError(t, err)
	again.Close()

	regular := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(regular, []byte("x"), 0o600))
	_, err = openSocket(listenSpec{Path: regular, UID: os.Getuid()})
	require.ErrorContains(t, err, "not a socket")
}

func TestServeAgent_needsASocket(t *testing.T) {
	require.Error(t, serveAgent(context.Background(), http.NewServeMux(), nil))
}
