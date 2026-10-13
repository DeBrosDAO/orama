package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

const (
	agentSignPath    = "/v1/orama/tx/sign"
	agentAccountPath = "/v1/orama/account"
	agentTimeout     = 30 * time.Second
	agentBodyLimit   = 1 << 16
	pubKeyLen        = 33
	sigLen           = 64
)

// remoteSigner signs SIGN_MODE_DIRECT sign bytes through the stagenet signing agent that a node
// runs beside its chain (chain/scripts/stagenet/node), reached over the forwarded unix socket. It
// is a tx.Signer: the transaction builder never sees a key.
type remoteSigner struct {
	client  *http.Client
	address string
	pub     cryptotypes.PubKey
}

type agentReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Data  struct {
		Signature string `json:"signature"`
		PubKey    string `json:"pubKey"`
		Address   string `json:"address"`
	} `json:"data"`
}

// dialSigner connects to the agent at socket and reads the account it signs for.
func dialSigner(ctx context.Context, socket string) (*remoteSigner, error) {
	s := &remoteSigner{client: &http.Client{
		Timeout: agentTimeout,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}},
	}}
	reply, err := s.call(ctx, http.MethodGet, agentAccountPath, nil)
	if err != nil {
		return nil, err
	}
	pub, err := decodePubKey(reply.Data.PubKey)
	if err != nil {
		return nil, err
	}
	s.pub, s.address = pub, reply.Data.Address
	return s, nil
}

func decodePubKey(b64 string) (cryptotypes.PubKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != pubKeyLen {
		return nil, errors.New("the agent's public key is not a 33-byte compressed secp256k1 key")
	}
	return &secp256k1.PubKey{Key: raw}, nil
}

func (s *remoteSigner) call(ctx context.Context, method, path string, body []byte) (agentReply, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, bytes.NewReader(body))
	if err != nil {
		return agentReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return agentReply{}, fmt.Errorf("signing agent %s: %w", path, err)
	}
	defer resp.Body.Close()
	var reply agentReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, agentBodyLimit)).Decode(&reply); err != nil {
		return agentReply{}, fmt.Errorf("signing agent %s answered HTTP %d with a body that is not its JSON: %w", path, resp.StatusCode, err)
	}
	if !reply.OK {
		return agentReply{}, fmt.Errorf("signing agent %s refused: %s", path, reply.Error)
	}
	return reply, nil
}

// AccountAddress is the orama address the agent signs for.
func (s *remoteSigner) AccountAddress() string { return s.address }

// PublicKey is that account's key.
func (s *remoteSigner) PublicKey() cryptotypes.PubKey { return s.pub }

// Sign asks the agent to sign signBytes and checks the answer verifies against the agent's own
// public key before returning it.
func (s *remoteSigner) Sign(signBytes []byte) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"signDoc": base64.StdEncoding.EncodeToString(signBytes)})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()
	reply, err := s.call(ctx, http.MethodPost, agentSignPath, body)
	if err != nil {
		return nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(reply.Data.Signature)
	if err != nil || len(sig) != sigLen {
		return nil, errors.New("the agent's signature is not 64 bytes")
	}
	if !s.pub.VerifySignature(signBytes, sig) {
		return nil, errors.New("the agent's signature does not verify against its own public key")
	}
	return sig, nil
}
