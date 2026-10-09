package chainread

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

const (
	simulateRoute  = "simulate"
	broadcastRoute = "broadcast"
)

// Coin is an amount of one denomination.
type Coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// SimulateResult is what the gateway's POST /v1/chain/simulate answers for a transaction the chain
// would run: the gas it wanted and used, and the fee at the chain's current base fee for the gas
// used. BaseFee is norama per unit of gas, for a caller that pads the gas limit.
type SimulateResult struct {
	GasWanted uint64 `json:"gas_wanted"`
	GasUsed   uint64 `json:"gas_used"`
	Fee       Coin   `json:"fee"`
	BaseFee   string `json:"base_fee"`
}

// BroadcastResult is what POST /v1/chain/broadcast answers for a transaction the chain's mempool
// took (code 0): its hash, to read from /v1/chain/tx?hash= until it is in a block.
type BroadcastResult struct {
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace"`
	Log       string `json:"log"`
	TxHash    string `json:"tx_hash"`
}

// TxRefusedError is a transaction the chain refused, on simulate or on broadcast (HTTP 422). The log
// is the gateway's sanitised copy of the chain's reason. TxHash is set on a broadcast.
type TxRefusedError struct {
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace"`
	Log       string `json:"log"`
	TxHash    string `json:"tx_hash"`
}

func (e *TxRefusedError) Error() string {
	return fmt.Sprintf("the chain refused the transaction (code %d %s): %s", e.Code, e.Codespace, e.Log)
}

// Simulate runs the signed transaction txRaw (the protobuf TxRaw bytes) through the gateway's
// POST /v1/chain/simulate. A transaction the chain refuses is a *TxRefusedError.
func (r *Reader) Simulate(ctx context.Context, txRaw []byte) (SimulateResult, error) {
	var out SimulateResult
	body, err := r.gatewayPostTx(ctx, simulateRoute, txRaw)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("simulate answer: %w", err)
	}
	return out, nil
}

// Broadcast submits the signed transaction txRaw through the gateway's POST /v1/chain/broadcast,
// which answers once the chain's mempool has checked it, not when it is in a block. A transaction
// the chain refuses is a *TxRefusedError.
func (r *Reader) Broadcast(ctx context.Context, txRaw []byte) (BroadcastResult, error) {
	var out BroadcastResult
	body, err := r.gatewayPostTx(ctx, broadcastRoute, txRaw)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("broadcast answer: %w", err)
	}
	return out, nil
}

func (r *Reader) gatewayPostTx(ctx context.Context, route string, txRaw []byte) (json.RawMessage, error) {
	root, err := base(r.Gateway, "--gateway")
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{"tx_bytes": base64.StdEncoding.EncodeToString(txRaw)})
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", route, err)
	}
	target := root + "/v1/chain/" + route
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", route, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", route, httputil.WithoutQuery(req.URL.String()), httputil.WithoutURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", route, httputil.WithoutQuery(req.URL.String()), httputil.WithoutURL(err))
	}
	if len(body) > responseLimit {
		return nil, fmt.Errorf("response from %s is over %d bytes", req.URL.Host, responseLimit)
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		var refused TxRefusedError
		if err := json.Unmarshal(body, &refused); err != nil {
			return nil, fmt.Errorf("%s answered HTTP 422 with a body that is not a refusal: %w", req.URL.Host, err)
		}
		return nil, &refused
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s answered HTTP %d: %s", req.URL.Host, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}
