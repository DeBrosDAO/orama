package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

// fakeAgent serves the agent protocol on a unix socket with the given key. tamper corrupts the
// signature it returns.
func fakeAgent(t *testing.T, tamper bool) (socket string, acct tx.Account) {
	t.Helper()
	acct, err := tx.DeriveAccount(secp256k1.GenPrivKey().Bytes())
	require.NoError(t, err)
	dir, err := os.MkdirTemp("/tmp", "sg")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	require.NoError(t, err)
	mux := http.NewServeMux()
	data := func(sig []byte) map[string]any {
		d := map[string]any{"pubKey": base64.StdEncoding.EncodeToString(acct.PublicKey().Bytes()), "address": acct.Address}
		if sig != nil {
			d["signature"] = base64.StdEncoding.EncodeToString(sig)
		}
		return d
	}
	mux.HandleFunc("GET "+agentAccountPath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data(nil)})
	})
	mux.HandleFunc("POST "+agentSignPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ SignDoc string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		doc, _ := base64.StdEncoding.DecodeString(body.SignDoc)
		sig, err := acct.Sign(doc)
		require.NoError(t, err)
		if tamper {
			sig[0] ^= 0xff
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data(sig)})
	})
	mux.HandleFunc("POST /refuse", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "locked"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return socket, acct
}

func TestRemoteSigner_signsAsTheAgentsAccount(t *testing.T) {
	socket, acct := fakeAgent(t, false)
	s, err := dialSigner(context.Background(), socket)
	require.NoError(t, err)
	require.Equal(t, acct.Address, s.AccountAddress())
	require.Equal(t, acct.PublicKey().Bytes(), s.PublicKey().Bytes())

	sig, err := s.Sign([]byte("sign bytes"))
	require.NoError(t, err)
	require.True(t, acct.PublicKey().VerifySignature([]byte("sign bytes"), sig))
}

func TestRemoteSigner_refusesASignatureThatDoesNotVerify(t *testing.T) {
	socket, _ := fakeAgent(t, true)
	s, err := dialSigner(context.Background(), socket)
	require.NoError(t, err)
	_, err = s.Sign([]byte("x"))
	require.ErrorContains(t, err, "does not verify")
}

func TestDialSigner_failsWhenNoAgentListens(t *testing.T) {
	_, err := dialSigner(context.Background(), filepath.Join(t.TempDir(), "none.sock"))
	require.Error(t, err)
}

func TestDecodePubKey_refusesTheWrongLength(t *testing.T) {
	_, err := decodePubKey(base64.StdEncoding.EncodeToString([]byte("short")))
	require.Error(t, err)
	_, err = decodePubKey("!!")
	require.Error(t, err)
}

func TestRemoteSigner_reportsAnAgentRefusal(t *testing.T) {
	socket, _ := fakeAgent(t, false)
	s, err := dialSigner(context.Background(), socket)
	require.NoError(t, err)
	_, err = s.call(context.Background(), http.MethodPost, "/refuse", []byte("{}"))
	require.ErrorContains(t, err, "locked")
}
