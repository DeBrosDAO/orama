package clusterreg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	submitTimeout = 10 * time.Second
	submitLimit   = 1 << 20
)

// Account is the on-chain base account a registration is signed as.
type Account struct {
	Number   uint64
	Sequence uint64
	// PubKey is set when the chain has seen this account sign. Nil when it has not.
	PubKey []byte
}

// FetchAccount reads account number, sequence, and pubkey from the Cosmos
// REST API. base is the API root, for example http://127.0.0.1:31003.
func FetchAccount(ctx context.Context, base, address string) (Account, error) {
	var acct Account
	body, err := getJSON(ctx, strings.TrimRight(base, "/")+"/cosmos/auth/v1beta1/accounts/"+address)
	if err != nil {
		return acct, err
	}
	var resp struct {
		Account struct {
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
			PubKey        *struct {
				Key string `json:"key"`
			} `json:"pub_key"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return acct, fmt.Errorf("account response is not JSON")
	}
	n, err := parseUint(resp.Account.AccountNumber)
	if err != nil {
		return acct, fmt.Errorf("account_number: %w", err)
	}
	seq, err := parseUint(resp.Account.Sequence)
	if err != nil {
		return acct, fmt.Errorf("sequence: %w", err)
	}
	acct.Number = n
	acct.Sequence = seq
	if resp.Account.PubKey != nil && resp.Account.PubKey.Key != "" {
		pub, err := base64.StdEncoding.DecodeString(resp.Account.PubKey.Key)
		if err != nil || len(pub) != 33 {
			return acct, fmt.Errorf("account pubkey is not a 33-byte key")
		}
		acct.PubKey = pub
	}
	return acct, nil
}

// Broadcast posts a signed tx and returns its hash. A non-zero code is an error.
func Broadcast(ctx context.Context, base string, tx []byte) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"tx_bytes": base64.StdEncoding.EncodeToString(tx),
		"mode":     "BROADCAST_MODE_SYNC",
	})
	if err != nil {
		return "", err
	}
	body, err := postJSON(ctx, strings.TrimRight(base, "/")+"/cosmos/tx/v1beta1/txs", payload)
	if err != nil {
		return "", err
	}
	var resp struct {
		TxResponse struct {
			Code   uint32 `json:"code"`
			TxHash string `json:"txhash"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("broadcast response is not JSON")
	}
	if resp.TxResponse.Code != 0 || resp.TxResponse.TxHash == "" {
		log := resp.TxResponse.RawLog
		if len(log) > 200 {
			log = log[:200]
		}
		return "", fmt.Errorf("broadcast rejected the tx (code %d): %s", resp.TxResponse.Code, log)
	}
	return resp.TxResponse.TxHash, nil
}

func parseUint(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%q is not an integer", s)
		}
		n = n*10 + uint64(s[i]-'0')
	}
	return n, nil
}

func getJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return doLimited(req)
}

func postJSON(ctx context.Context, url string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return doLimited(req)
}

type httpClientKey struct{}

// WithHTTPClient makes every chain request made with ctx use client instead
// of http.DefaultClient, and the client's Timeout instead of the default
// request timeout when it is set. An onion submission passes a client whose
// only route to the network is a Tor SOCKS proxy.
func WithHTTPClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, httpClientKey{}, client)
}

// StatusError is an HTTP error status from the chain API. Message is what the
// node said, control characters removed and cut short, empty when it said
// nothing readable.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("chain API returned HTTP %d", e.Code) }

func doLimited(req *http.Request) ([]byte, error) {
	client, timeout := http.DefaultClient, submitTimeout
	if c, ok := req.Context().Value(httpClientKey{}).(*http.Client); ok && c != nil {
		client = c
		if c.Timeout > 0 {
			timeout = c.Timeout
		}
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, submitLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > submitLimit {
		return nil, fmt.Errorf("response from the chain is over %d bytes", submitLimit)
	}
	if resp.StatusCode >= 400 {
		return nil, &StatusError{Code: resp.StatusCode, Message: errorMessage(body)}
	}
	return body, nil
}

// maxErrorMessage bounds how much of a node's error a StatusError keeps.
const maxErrorMessage = 300

// errorMessage reads the "message" of the gRPC-gateway error a node answers
// with, or the start of the body when it is not one.
func errorMessage(body []byte) string {
	var doc struct {
		Message string `json:"message"`
	}
	text := string(body)
	if json.Unmarshal(body, &doc) == nil && doc.Message != "" {
		text = doc.Message
	}
	runes := []rune(printable(text))
	if len(runes) > maxErrorMessage {
		runes = runes[:maxErrorMessage]
	}
	return string(runes)
}
