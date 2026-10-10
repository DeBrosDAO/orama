package nodenames

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

const (
	// queryNodeNames is the x/nodes query that pages every claimed name in name order.
	queryNodeNames = "orama.nodes.v1.Query/NodeNames"
	// PageLimit is the most names one NodeNames page holds; the chain clamps a larger request to it.
	PageLimit = 1000

	// statusPath is CometBFT's status on the node's RPC; statusRoute is the gateway's copy of it.
	statusPath  = "/status"
	statusRoute = "status"
)

// Page is one page of claimed names. NextKey is empty on the last page.
type Page struct {
	Nodes   []Named
	NextKey string
}

// Chain pages the claimed names. pageKey is the previous page's NextKey, empty for the first page.
type Chain interface {
	NodeNames(ctx context.Context, pageKey string) (Page, error)
	// CatchingUp reports whether the chain node is still catching up to the chain: what it answers
	// may be old, so a sync does not remove names on its word.
	CatchingUp(ctx context.Context) (bool, error)
}

// ReaderChain reads the names through a chainread.Reader: the co-located node's CometBFT RPC when
// the Reader has one, the gateway's public query route otherwise.
type ReaderChain struct{ Reader *chainread.Reader }

type nodeNamesRequest struct {
	Pagination nodeNamesPagination `json:"pagination"`
}

type nodeNamesPagination struct {
	Key   string `json:"key,omitempty"`
	Limit uint64 `json:"limit"`
}

type nodeNamesResponse struct {
	Nodes []struct {
		Name   string   `json:"name"`
		NodeID string   `json:"node_id"`
		IPs    []string `json:"ips"`
	} `json:"nodes"`
	Pagination struct {
		NextKey string `json:"next_key"`
	} `json:"pagination"`
}

// statusAnswer is CometBFT's status, bare or inside the JSON-RPC envelope the gateway forwards.
type statusAnswer struct {
	SyncInfo *struct {
		CatchingUp bool `json:"catching_up"`
	} `json:"sync_info"`
	Result *struct {
		SyncInfo *struct {
			CatchingUp bool `json:"catching_up"`
		} `json:"sync_info"`
	} `json:"result"`
}

func (c ReaderChain) CatchingUp(ctx context.Context) (bool, error) {
	var raw json.RawMessage
	var err error
	if c.Reader.RPC != "" {
		raw, err = c.Reader.RPCGet(ctx, statusPath)
	} else {
		raw, err = c.Reader.GatewayGet(ctx, statusRoute)
	}
	if err != nil {
		return false, fmt.Errorf("read the chain node's status: %w", err)
	}
	var st statusAnswer
	if err := json.Unmarshal(raw, &st); err != nil {
		return false, fmt.Errorf("the chain node's status is not JSON: %w", err)
	}
	switch {
	case st.SyncInfo != nil:
		return st.SyncInfo.CatchingUp, nil
	case st.Result != nil && st.Result.SyncInfo != nil:
		return st.Result.SyncInfo.CatchingUp, nil
	}
	return false, errors.New("the chain node's status has no sync_info, so it cannot be known to be caught up")
}

func (c ReaderChain) NodeNames(ctx context.Context, pageKey string) (Page, error) {
	request, err := json.Marshal(nodeNamesRequest{Pagination: nodeNamesPagination{Key: pageKey, Limit: PageLimit}})
	if err != nil {
		return Page{}, fmt.Errorf("encode the NodeNames request: %w", err)
	}
	raw, err := c.Reader.Query(ctx, queryNodeNames, string(request))
	if err != nil {
		return Page{}, fmt.Errorf("read the claimed node names from the chain: %w", err)
	}
	var resp nodeNamesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Page{}, fmt.Errorf("the chain answered NodeNames with something that is not JSON: %w", err)
	}
	page := Page{NextKey: resp.Pagination.NextKey}
	for _, n := range resp.Nodes {
		page.Nodes = append(page.Nodes, Named{Name: n.Name, NodeID: n.NodeID, IPs: n.IPs})
	}
	return page, nil
}
