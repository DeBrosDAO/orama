// Package statesync finds what a joining chain node starts from: a block two
// independent seeds agree on, read through each seed's light-client route
// (/v1/chain/light on its gateway), and the nodes to restore a snapshot from.
//
// CometBFT state sync needs a trusted height and hash that were not told to it
// by the node it fetches a snapshot from. This package asks two seeds, which the
// network's manifest names by DNS name, for the same block and accepts it only
// when their answers are the same hash for the same height.
package statesync

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// LightPath is the gateway route that serves the light-client JSON-RPC.
	LightPath = "/v1/chain/light"
	// requestTimeout bounds one light-client call.
	requestTimeout = 20 * time.Second
	// maxResponseBytes bounds an answer: a commit with a hundred signatures is
	// tens of kilobytes.
	maxResponseBytes = 4 << 20
	maxRedirects     = 5
)

// Status is the part of a node's CometBFT status a joiner reads.
type Status struct {
	NodeID       string
	Network      string
	LatestHeight int64
	CatchingUp   bool
}

// Doer sends one HTTP request. *http.Client is one; a test passes a TLS test
// server's client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is one seed's light-client route.
type Client struct {
	// BaseURL is the seed's gateway, https://<seed>.
	BaseURL string
	HTTP    Doer
}

// NewHTTPClient is the client the seeds are asked with: bounded in time, and
// following a redirect only to the same host over https. A seed that answers
// from another host is not the seed the manifest named, and the two seeds'
// agreement means something only if each answer is its own.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("refusing a redirect to %s: only https is followed", req.URL.Redacted())
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing a redirect from %s to %s: a seed answers for itself", via[0].URL.Host, req.URL.Host)
			}
			return nil
		},
	}
}

// Endpoint is the light-client URL a node's [statesync] rpc_servers names.
func (c Client) Endpoint() string { return strings.TrimRight(c.BaseURL, "/") + LightPath }

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcAnswer struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error"`
}

// call posts one JSON-RPC request and returns its result.
func (c Client) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	u, err := url.Parse(c.Endpoint())
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("light-client endpoint %q is not an https:// URL", c.Endpoint())
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode the %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build the %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s (%s): %w", u, method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("read the %s answer of %s: %w", method, u, errors.Join(err, errTooLarge(len(raw))))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST %s (%s): HTTP %d", u, method, resp.StatusCode)
	}
	var ans rpcAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("the %s answer of %s is not JSON-RPC: %w", method, u, err)
	}
	if ans.Error != nil {
		return nil, fmt.Errorf("%s of %s: %s %s", method, u, ans.Error.Message, ans.Error.Data)
	}
	return ans.Result, nil
}

func errTooLarge(n int) error {
	if n > maxResponseBytes {
		return fmt.Errorf("the answer is larger than %d bytes", maxResponseBytes)
	}
	return nil
}

// Status reads the seed's node id, chain id and height.
func (c Client) Status(ctx context.Context) (Status, error) {
	result, err := c.call(ctx, "status", nil)
	if err != nil {
		return Status{}, err
	}
	var doc struct {
		NodeInfo struct {
			ID      string `json:"id"`
			Network string `json:"network"`
		} `json:"node_info"`
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
			CatchingUp        bool   `json:"catching_up"`
		} `json:"sync_info"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		return Status{}, fmt.Errorf("the status of %s: %w", c.BaseURL, err)
	}
	height, err := strconv.ParseInt(doc.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil || height < 1 {
		return Status{}, fmt.Errorf("the status of %s has latest_block_height %q", c.BaseURL, doc.SyncInfo.LatestBlockHeight)
	}
	if !nodeIDPattern(doc.NodeInfo.ID) {
		return Status{}, fmt.Errorf("the status of %s has node id %q, not 40 hex characters", c.BaseURL, doc.NodeInfo.ID)
	}
	return Status{NodeID: doc.NodeInfo.ID, Network: doc.NodeInfo.Network, LatestHeight: height, CatchingUp: doc.SyncInfo.CatchingUp}, nil
}

// CommitHash reads the hash of the block at height from the seed's signed
// header: the hash a joiner's light client verifies the chain forward from.
func (c Client) CommitHash(ctx context.Context, height int64) (string, error) {
	result, err := c.call(ctx, "commit", map[string]any{"height": strconv.FormatInt(height, 10)})
	if err != nil {
		return "", err
	}
	var doc struct {
		SignedHeader struct {
			Header struct {
				Height string `json:"height"`
			} `json:"header"`
			Commit struct {
				BlockID struct {
					Hash string `json:"hash"`
				} `json:"block_id"`
			} `json:"commit"`
		} `json:"signed_header"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		return "", fmt.Errorf("the commit of %s at %d: %w", c.BaseURL, height, err)
	}
	if got := doc.SignedHeader.Header.Height; got != strconv.FormatInt(height, 10) {
		return "", fmt.Errorf("%s answered for height %q when asked for %d", c.BaseURL, got, height)
	}
	hash := strings.ToLower(doc.SignedHeader.Commit.BlockID.Hash)
	if b, err := hex.DecodeString(hash); err != nil || len(b) != 32 {
		return "", fmt.Errorf("the commit of %s at %d has block hash %q, not a 32-byte hex hash", c.BaseURL, height, hash)
	}
	return hash, nil
}

// nodeIDPattern is a CometBFT node id: 20 bytes in hex.
func nodeIDPattern(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 20 && id == strings.ToLower(id)
}
