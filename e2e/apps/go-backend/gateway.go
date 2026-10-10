package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// caPEM is the trust the deploying test puts next to the source: the
// cluster's certificate chains to it (Let's Encrypt staging on a test fleet,
// which no system store trusts). A real app on a production cluster would
// use the system roots.
//
//go:embed ca.pem
var caPEM []byte

// gateway is this app's client of its namespace gateway, as itself: the
// workload token the platform hands it, renewed before it expires
// (website/src/docs/developer/deployments.mdx "Your app's own credential").
type gateway struct {
	base   string
	http   *http.Client
	mu     sync.Mutex
	token  string
	renews int
}

func newGateway() (*gateway, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("ca.pem holds no certificate")
	}
	raw, err := os.ReadFile(os.Getenv("ORAMA_TOKEN_FILE"))
	if err != nil {
		return nil, fmt.Errorf("read the workload token: %w", err)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return &gateway{
		base:  strings.TrimRight(os.Getenv("ORAMA_GATEWAY_URL"), "/"),
		http:  &http.Client{Transport: tr, Timeout: 30 * time.Second},
		token: strings.TrimSpace(string(raw)),
	}, nil
}

func (g *gateway) current() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.token
}

// renew exchanges the current token for the next one.
func (g *gateway) renew() (renewResult, error) {
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := g.call(http.MethodPost, "/v1/auth/renew", "", nil, &out); err != nil {
		return renewResult{}, err
	}
	if out.AccessToken == "" {
		return renewResult{}, fmt.Errorf("renew answered no access_token")
	}
	g.mu.Lock()
	changed := out.AccessToken != g.token
	g.token = out.AccessToken
	g.renews++
	res := renewResult{Changed: changed, ExpiresIn: out.ExpiresIn, Renewals: g.renews}
	g.mu.Unlock()
	// Prove the renewed token is accepted before reporting success.
	if err := g.call(http.MethodGet, "/v1/auth/whoami", "", nil, &res.Who); err != nil {
		return res, fmt.Errorf("the renewed token is refused: %w", err)
	}
	return res, nil
}

type renewResult struct {
	Changed   bool `json:"changed"`
	ExpiresIn int  `json:"expires_in"`
	Renewals  int  `json:"renewals"`
	Who       struct {
		Principal string   `json:"principal"`
		Role      string   `json:"role"`
		Grants    []string `json:"grants"`
	} `json:"who"`
}

// renewLoop renews at half the token's life, as the guide asks.
func (g *gateway) renewLoop() {
	wait := time.Minute
	for {
		time.Sleep(wait)
		res, err := g.renew()
		if err != nil {
			fmt.Fprintln(os.Stderr, "renew failed:", err)
			wait = time.Minute
			continue
		}
		wait = time.Duration(max(res.ExpiresIn/2, 30)) * time.Second
	}
}

// gatewayError is a non-2xx answer, kept so the app can pass the status on.
type gatewayError struct {
	Status int
	Body   string
}

func (e *gatewayError) Error() string {
	return fmt.Sprintf("gateway answered %d: %.200s", e.Status, e.Body)
}

// call sends body (JSON, or raw with contentType set) as the app and decodes
// a JSON answer into out when out is not nil.
func (g *gateway) call(method, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequest(method, g.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.current())
	if body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &gatewayError{Status: resp.StatusCode, Body: string(data)}
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}

func (g *gateway) postJSON(path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return g.call(http.MethodPost, path, "", body, out)
}

// upload stores data in the namespace's IPFS as the app.
func (g *gateway) upload(name string, data []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	var out struct {
		Cid string `json:"cid"`
	}
	if err := g.call(http.MethodPost, "/v1/storage/upload", w.FormDataContentType(), buf.Bytes(), &out); err != nil {
		return "", err
	}
	return out.Cid, nil
}
