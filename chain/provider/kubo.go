package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Kubo is the public Kubo daemon on this host, reached through its
// token-gated loopback RPC. It is a different daemon from any private
// cluster's Kubo: it has its own swarm and repo, and only public deal classes
// (PUBLIC_PIN and ARCHIVE) ever reach it.
type Kubo struct {
	base  string
	token string
	hc    *http.Client
}

const (
	// kuboCallTimeout bounds one RPC call, including a fetch from the swarm.
	kuboCallTimeout = 2 * time.Minute
	// kuboErrBody is how much of a failing response is quoted in the error.
	kuboErrBody = 512
	// kuboAddQuery makes a CIDv1 with raw leaves and pins it, so the same
	// bytes always get the same CID and a client can compute it.
	kuboAddQuery = "pin=true&cid-version=1&raw-leaves=true"
)

// cidPattern accepts CIDv0 (base58btc, Qm...) and CIDv1 base32 (b...). Nothing
// else reaches a Kubo RPC path.
var cidPattern = regexp.MustCompile(`^(Qm[1-9A-HJ-NP-Za-km-z]{44}|b[a-z2-7]{50,120})$`)

// ValidIPFSCID reports whether s is a CIDv0 or a base32 CIDv1.
func ValidIPFSCID(s string) bool { return cidPattern.MatchString(s) }

// NewKubo returns a client for the RPC at apiURL, which must be a loopback
// http URL: the bearer token is never sent anywhere else.
func NewKubo(apiURL, token string) (*Kubo, error) {
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("kubo RPC %q must be an http://127.0.0.1:<port> URL", apiURL)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("kubo RPC %q has no port: %w", apiURL, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("kubo RPC host %q is not a loopback address; the token is sent only to this host's own Kubo", host)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("kubo RPC token is empty")
	}
	return &Kubo{base: "http://" + u.Host, token: token, hc: &http.Client{
		Timeout:       kuboCallTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Add stores data in Kubo as a CIDv1 (raw leaves), pins it and returns the CID.
func (k *Kubo) Add(ctx context.Context, data []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "piece")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	resp, err := k.post(ctx, "/api/v0/add?"+kuboAddQuery, mw.FormDataContentType(), &body)
	if err != nil {
		return "", fmt.Errorf("kubo add: %w", err)
	}
	defer resp.Body.Close()
	var out struct{ Hash string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, kuboErrBody*4)).Decode(&out); err != nil {
		return "", fmt.Errorf("kubo add: read the CID: %w", err)
	}
	if !ValidIPFSCID(out.Hash) {
		return "", fmt.Errorf("kubo add returned %q, which is not a CID", out.Hash)
	}
	return out.Hash, nil
}

// Cat reads at most max bytes of cid, fetching missing blocks from the swarm.
// Content longer than max is an error, not a truncation.
func (k *Kubo) Cat(ctx context.Context, cid string, max int64) ([]byte, error) {
	if !ValidIPFSCID(cid) {
		return nil, fmt.Errorf("%q is not a CID", cid)
	}
	resp, err := k.post(ctx, "/api/v0/cat?arg="+url.QueryEscape(cid)+"&length="+fmt.Sprint(max+1), "", nil)
	if err != nil {
		return nil, fmt.Errorf("kubo cat %s: %w", cid, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("kubo cat %s: %w", cid, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than the %d bytes a piece may be", cid, max)
	}
	return data, nil
}

// Pin pins cid recursively. Its blocks must already be local: the provider
// calls it only after Cat read the whole piece.
func (k *Kubo) Pin(ctx context.Context, cid string) error {
	return k.pinCall(ctx, "pin/add", cid)
}

// Unpin removes the pin on cid. Kubo's GC then reclaims the blocks. A CID
// that is not pinned is not an error.
func (k *Kubo) Unpin(ctx context.Context, cid string) error {
	err := k.pinCall(ctx, "pin/rm", cid)
	if err != nil && strings.Contains(err.Error(), "not pinned") {
		return nil
	}
	return err
}

func (k *Kubo) pinCall(ctx context.Context, call, cid string) error {
	if !ValidIPFSCID(cid) {
		return fmt.Errorf("%q is not a CID", cid)
	}
	resp, err := k.post(ctx, "/api/v0/"+call+"?arg="+url.QueryEscape(cid), "", nil)
	if err != nil {
		return fmt.Errorf("kubo %s %s: %w", call, cid, err)
	}
	return resp.Body.Close()
}

// post sends one RPC call with the bearer token. A non-200 status is an error
// carrying the start of Kubo's message.
func (k *Kubo) post(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach the public Kubo at %s (is orama-global-ipfs.service running?): %w", k.base, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, kuboErrBody))
		resp.Body.Close()
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}
