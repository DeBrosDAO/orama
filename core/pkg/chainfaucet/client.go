package chainfaucet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

const (
	// RoutePath is the faucet route of a gateway.
	RoutePath = "/v1/chain/faucet"
	// maxAnswerBytes bounds a gateway's answer: a drip is a few hundred bytes.
	maxAnswerBytes = 16 << 10
)

var txHashPattern = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

// Doer sends one HTTP request. *http.Client is one.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrorAnswer is what a gateway answers when it was reached and gave no drip and no typed
// refusal: it serves no faucet (a 404), or it answered something else.
type ErrorAnswer struct {
	Host   string
	Status int
	Detail string
}

func (e *ErrorAnswer) Error() string {
	if e.Status == http.StatusNotFound {
		return fmt.Sprintf("%s serves no faucet (HTTP 404)", e.Host)
	}
	return fmt.Sprintf("%s answered HTTP %d: %s", e.Host, e.Status, e.Detail)
}

// Request asks the faucet of the gateway at base (https://host) for amount norama for recipient and
// returns the drip once it is in a block. A refusal the gateway explains is a *Refusal; a gateway
// that has no faucet, or answers something else, is an *ErrorAnswer. Everything in the answer is
// untrusted text and is cleaned; the caller checks the recipient's balance, not this answer, to
// know it was paid.
func Request(ctx context.Context, doer Doer, base, recipient string, amount *big.Int) (*Dripped, error) {
	endpoint, host, err := faucetURL(base)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"recipient": recipient, "amount": amount.String()})
	if err != nil {
		return nil, fmt.Errorf("encode the faucet request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build the faucet request to %s: %w", host, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask the faucet of %s: %w", host, httputil.WithoutURL(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil || len(raw) > maxAnswerBytes {
		return nil, &ErrorAnswer{Host: host, Status: resp.StatusCode, Detail: "no usable answer"}
	}
	if resp.StatusCode == http.StatusOK {
		return parseDrip(host, raw, amount)
	}
	return nil, failureOf(host, resp.StatusCode, raw)
}

// faucetURL is the route on the gateway at base, which must be https (http only for this machine).
func faucetURL(base string) (endpoint, host string, err error) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return "", "", fmt.Errorf("%q is not a gateway URL (https://host)", httputil.Printable(base))
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return "", "", fmt.Errorf("%q is not https: the request names the account to fund and the answer says whether it was paid", httputil.Printable(base))
	}
	return strings.TrimRight(base, "/") + RoutePath, u.Host, nil
}

func isLoopbackHost(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }

func parseDrip(host string, raw []byte, asked *big.Int) (*Dripped, error) {
	var doc struct {
		TxHash string `json:"tx_hash"`
		Amount string `json:"amount"`
		Height string `json:"height"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, &ErrorAnswer{Host: host, Status: http.StatusOK, Detail: "the answer is not a drip"}
	}
	height, herr := strconv.ParseInt(doc.Height, 10, 64)
	amount, ok := new(big.Int).SetString(doc.Amount, 10)
	if !txHashPattern.MatchString(doc.TxHash) || herr != nil || height <= 0 || !ok || amount.Cmp(asked) != 0 {
		return nil, &ErrorAnswer{Host: host, Status: http.StatusOK, Detail: "the answer is not the drip that was asked for"}
	}
	return &Dripped{TxHash: strings.ToUpper(doc.TxHash), Height: height, Amount: amount}, nil
}

// failureOf reads a non-200 answer: a typed refusal when it is one, else the status.
func failureOf(host string, status int, raw []byte) error {
	var doc struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &doc) == nil && doc.Error != "" {
		return &Refusal{Kind: Kind(httputil.PrintableMax(doc.Error, maxKindLen)), Message: httputil.PrintableMax(httputil.OneLine(doc.Message), maxDetail)}
	}
	return &ErrorAnswer{Host: host, Status: status, Detail: httputil.PrintableMax(httputil.OneLine(string(raw)), maxDetail)}
}

// maxKindLen bounds the kind a gateway names in a refusal.
const maxKindLen = 32
