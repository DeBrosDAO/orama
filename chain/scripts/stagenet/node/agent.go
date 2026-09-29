package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

const (
	// signPath is the RootWallet agent's SIGN_MODE_DIRECT route (core/pkg/rwagent), which the
	// `orama global` and `orama storage` commands call through RW_AGENT_SOCK.
	signPath = "/v1/orama/tx/sign"
	// accountPath is an addition of this agent: the address and public key it signs for, which the
	// smoke test needs to build a transaction before asking for its signature.
	accountPath = "/v1/orama/account"
	// maxSignDocBytes bounds a request body. A SignDoc of one message is a few KiB.
	maxSignDocBytes = 1 << 20
	// serveShutdownTimeout is how long the agent lets an in-flight signature finish on a stop.
	serveShutdownTimeout = 5 * time.Second
	socketMode           = 0o600
	socketDirMode        = 0o700
)

// agentEnvelope is the RootWallet agent's JSON envelope (core/pkg/rwagent apiResponse).
type agentEnvelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

type signData struct {
	Signature string `json:"signature"`
	PubKey    string `json:"pubKey"`
	Address   string `json:"address"`
}

// newAgentHandler signs SIGN_MODE_DIRECT sign documents with acct, and only that: it never
// returns the key, and it signs nothing but the bytes it is sent. It stands in for the RootWallet
// agent on a stagenet node, where the operator key is a test-keyring key that holds no real value;
// the same protocol means the real `orama` commands run unchanged.
func newAgentHandler(acct tx.Account) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+signPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SignDoc string `json:"signDoc"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxSignDocBytes)).Decode(&body); err != nil {
			writeAgent(w, http.StatusBadRequest, agentEnvelope{Error: "the body is not the signing request JSON", Code: "INVALID_REQUEST"})
			return
		}
		doc, err := base64.StdEncoding.DecodeString(body.SignDoc)
		if err != nil || len(doc) == 0 {
			writeAgent(w, http.StatusBadRequest, agentEnvelope{Error: "signDoc is not base64 bytes", Code: "INVALID_REQUEST"})
			return
		}
		sig, err := acct.Sign(doc)
		if err != nil {
			writeAgent(w, http.StatusInternalServerError, agentEnvelope{Error: "sign: " + err.Error(), Code: "INTERNAL_ERROR"})
			return
		}
		writeAgent(w, http.StatusOK, agentEnvelope{OK: true, Data: accountData(acct, sig)})
	})
	mux.HandleFunc("GET "+accountPath, func(w http.ResponseWriter, _ *http.Request) {
		writeAgent(w, http.StatusOK, agentEnvelope{OK: true, Data: accountData(acct, nil)})
	})
	return mux
}

func accountData(acct tx.Account, sig []byte) signData {
	d := signData{PubKey: base64.StdEncoding.EncodeToString(acct.PublicKey().Bytes()), Address: acct.Address}
	if sig != nil {
		d.Signature = base64.StdEncoding.EncodeToString(sig)
	}
	return d
}

func writeAgent(w http.ResponseWriter, status int, env agentEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// listenSpec is one socket the agent serves: path:uid. The uid owns the socket and its directory,
// because `orama` refuses an agent socket another user owns, and an ssh forward reaches it as the
// login user.
type listenSpec struct {
	Path string
	UID  int
}

// parseListen reads "path:uid". The uid is required: a socket with no stated owner is a mistake.
func parseListen(s string) (listenSpec, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return listenSpec{}, fmt.Errorf("--listen %q is not path:uid", s)
	}
	uid, err := strconv.Atoi(s[i+1:])
	if err != nil || uid < 0 {
		return listenSpec{}, fmt.Errorf("--listen %q: %q is not a uid", s, s[i+1:])
	}
	path := s[:i]
	if !filepath.IsAbs(path) {
		return listenSpec{}, fmt.Errorf("--listen %q: the socket path must be absolute", s)
	}
	return listenSpec{Path: path, UID: uid}, nil
}

// openSocket creates the socket owned by spec.UID, mode 0600, in a directory of the same owner.
// A stale socket file from an earlier run is replaced; anything else at the path is an error.
func openSocket(spec listenSpec) (net.Listener, error) {
	dir := filepath.Dir(spec.Path)
	if err := os.MkdirAll(dir, socketDirMode); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chown(dir, spec.UID, -1); err != nil {
		return nil, fmt.Errorf("chown %s: %w", dir, err)
	}
	if fi, err := os.Lstat(spec.Path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", spec.Path)
		}
		if err := os.Remove(spec.Path); err != nil {
			return nil, fmt.Errorf("remove the stale socket %s: %w", spec.Path, err)
		}
	}
	ln, err := net.Listen("unix", spec.Path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", spec.Path, err)
	}
	if err := os.Chmod(spec.Path, socketMode); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", spec.Path, err)
	}
	if err := os.Chown(spec.Path, spec.UID, -1); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chown %s: %w", spec.Path, err)
	}
	return ln, nil
}

// serveAgent serves handler on every socket until ctx is done.
func serveAgent(ctx context.Context, handler http.Handler, specs []listenSpec) error {
	if len(specs) == 0 {
		return errors.New("at least one --listen path:uid is required")
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, len(specs))
	for _, spec := range specs {
		ln, err := openSocket(spec)
		if err != nil {
			return err
		}
		defer os.Remove(spec.Path)
		go func() { errc <- srv.Serve(ln) }()
	}
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errc:
		return err
	}
}
