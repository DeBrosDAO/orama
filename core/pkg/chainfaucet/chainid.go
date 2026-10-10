package chainfaucet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/netclass"
)

// nodeInfoPath is a node's Cosmos REST route for its chain id.
const nodeInfoPath = "/cosmos/base/tendermint/v1beta1/node_info"

// RESTChainID reads the chain id from a node's Cosmos REST API.
type RESTChainID struct {
	// Base is the API root, for example http://198.18.0.2:31003.
	Base string
	// HTTP is the client the read uses; nil is one with a request timeout.
	HTTP *http.Client
}

// ChainID is the node's network name.
func (r RESTChainID) ChainID(ctx context.Context) (string, error) {
	raw, err := (&chainread.Reader{REST: r.Base, HTTP: r.HTTP}).RESTGet(ctx, nodeInfoPath)
	if err != nil {
		return "", err
	}
	var doc struct {
		Info struct {
			Network string `json:"network"`
		} `json:"default_node_info"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("the node's info is not the JSON expected: %w", err)
	}
	if !netclass.ValidChainID(doc.Info.Network) {
		return "", fmt.Errorf("the node reports the chain id %q, which is not a chain id", httputil.Printable(doc.Info.Network))
	}
	return doc.Info.Network, nil
}
