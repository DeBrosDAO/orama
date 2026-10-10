// Package storageclient uploads sealed storage slots to the providers the
// chain assigned and fetches them back. It reads the chain through oramad's
// CometBFT RPC with hand-decoded protobuf, because core does not import the
// chain module. It never holds a signing key: a deal is created and signed
// elsewhere (orama storage create through the RootWallet agent).
package storageclient

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	chainTimeout  = 15 * time.Second
	chainMaxBody  = 4 << 20
	querySlot     = "/orama.storage.v1.Query/Slot"
	queryDeal     = "/orama.storage.v1.Query/Deal"
	queryNode     = "/orama.nodes.v1.Query/Node"
	slotAssigned  = 2
	slotActive    = 3
	storageIDMax  = 128
	providerHTTP  = "http://"
	providerHTTPS = "https://"
	// sdkCodespace and codeKeyNotFound are the SDK's ErrKeyNotFound, which
	// baseapp returns for a gRPC NotFound status.
	sdkCodespace    = "sdk"
	codeKeyNotFound = 22
)

// ErrNotFound is a deal, slot or node the chain does not have.
var ErrNotFound = errors.New("not found on chain")

// Chain reads x/storage and x/nodes through one CometBFT RPC endpoint.
type Chain struct {
	rpc    string
	client *http.Client
}

// NewChain takes oramad's RPC address, for example http://127.0.0.1:31001.
// A tcp:// address is read as http://.
func NewChain(rpcAddr string) (*Chain, error) {
	addr := strings.TrimSpace(rpcAddr)
	addr = strings.Replace(addr, "tcp://", "http://", 1)
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("chain rpc %q is not an http(s) address", rpcAddr)
	}
	return &Chain{rpc: strings.TrimRight(addr, "/"), client: &http.Client{Timeout: chainTimeout}}, nil
}

// Deal is the part of orama.storage.v1.Deal a client needs.
type Deal struct {
	ID       uint64
	Nonce    []byte
	Replicas uint32
}

// Slot is the part of orama.storage.v1.Slot a client needs.
type Slot struct {
	DealID    uint64
	Index     uint32
	NodeID    string
	PieceRoot []byte
	Status    int32
	Accepted  bool
}

// Held reports whether a node is assigned to the slot and may hold it.
func (s Slot) Held() bool {
	return s.NodeID != "" && (s.Status == slotAssigned || s.Status == slotActive)
}

// Deal reads one deal.
func (c *Chain) Deal(ctx context.Context, id uint64) (Deal, error) {
	body, err := c.query(ctx, queryDeal, encodeUintField(1, id))
	if err != nil {
		return Deal{}, err
	}
	return decodeDeal(body)
}

// Slot reads one deal slot.
func (c *Chain) Slot(ctx context.Context, dealID uint64, slot uint32) (Slot, error) {
	req := append(encodeUintField(1, dealID), encodeUintField(2, uint64(slot))...)
	body, err := c.query(ctx, querySlot, req)
	if err != nil {
		return Slot{}, err
	}
	return decodeSlot(body)
}

// ProviderURL returns the node's first http or https endpoint, the provider
// HTTP root that serves /pieces/.
func (c *Chain) ProviderURL(ctx context.Context, nodeID string) (string, error) {
	if nodeID == "" || len(nodeID) > storageIDMax {
		return "", fmt.Errorf("node id %q is not valid", nodeID)
	}
	body, err := c.query(ctx, queryNode, encodeStringField(1, nodeID))
	if err != nil {
		return "", err
	}
	endpoints, err := decodeNodeEndpoints(body)
	if err != nil {
		return "", err
	}
	for _, ep := range endpoints {
		if strings.HasPrefix(ep, providerHTTP) || strings.HasPrefix(ep, providerHTTPS) {
			return strings.TrimRight(ep, "/"), nil
		}
	}
	return "", fmt.Errorf("node %s names no http(s) provider endpoint in x/nodes", nodeID)
}

func (c *Chain) query(ctx context.Context, path string, req []byte) ([]byte, error) {
	q := url.Values{}
	q.Set("path", `"`+path+`"`)
	q.Set("data", "0x"+hex.EncodeToString(req))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rpc+"/abci_query?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("query %s at %s: %w", path, c.rpc, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, chainMaxBody))
	if err != nil {
		return nil, fmt.Errorf("read %s answer: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query %s: HTTP %d", path, resp.StatusCode)
	}
	return parseABCIAnswer(path, raw)
}

func parseABCIAnswer(path string, raw []byte) ([]byte, error) {
	var out struct {
		Result *struct {
			Response struct {
				Codespace string `json:"codespace"`
				Code      uint32 `json:"code"`
				Log       string `json:"log"`
				Value     string `json:"value"`
			} `json:"response"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("query %s: answer is not JSON-RPC", path)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("query %s: %s %s", path, out.Error.Message, out.Error.Data)
	}
	if out.Result == nil {
		return nil, fmt.Errorf("query %s: answer has no result", path)
	}
	r := out.Result.Response
	if r.Code != 0 {
		if r.Codespace == sdkCodespace && r.Code == codeKeyNotFound {
			return nil, fmt.Errorf("%w: %s: %s", ErrNotFound, path, r.Log)
		}
		return nil, fmt.Errorf("query %s failed with code %d: %s", path, r.Code, r.Log)
	}
	value, err := base64.StdEncoding.DecodeString(r.Value)
	if err != nil {
		return nil, fmt.Errorf("query %s: value is not base64", path)
	}
	return value, nil
}
