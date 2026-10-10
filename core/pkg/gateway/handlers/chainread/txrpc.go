package chainread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// txHashOf is the hash CometBFT and the explorer know a transaction by: the SHA-256 of its bytes,
// upper-case hex.
func txHashOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// simulateRequest is a cosmos.tx.v1beta1.SimulateRequest carrying raw as tx_bytes.
func simulateRequest(raw []byte) []byte {
	b := protowire.AppendTag(nil, simulateTxBytesField, protowire.BytesType)
	return protowire.AppendBytes(b, raw)
}

// parseGasUsed reads gas_info.gas_used from a cosmos.tx.v1beta1.SimulateResponse. It does not read
// gas_info.gas_wanted: simulation meters with no limit, so the SDK reports the unlimited meter's limit,
// the largest uint64, there (declaredGasLimit is the gas_wanted the route answers).
func parseGasUsed(resp []byte) (uint64, error) {
	info, ok, err := bytesField(resp, simulateGasInfoField)
	if err != nil {
		return 0, fmt.Errorf("simulate response: %w", err)
	}
	if !ok {
		return 0, errors.New("simulate response has no gas_info")
	}
	used, _, err := varintField(info, gasUsedField)
	if err != nil {
		return 0, fmt.Errorf("gas_info: %w", err)
	}
	return used, nil
}

// declaredGasLimit reads the gas limit a signed transaction declares: TxRaw.auth_info_bytes, its
// AuthInfo.fee and that Fee.gas_limit. A transaction that leaves any of them out declares 0, the
// proto default.
func declaredGasLimit(raw []byte) (uint64, error) {
	authInfo, ok, err := bytesField(raw, txRawAuthInfoField)
	if err != nil {
		return 0, fmt.Errorf("transaction: %w", err)
	}
	if !ok {
		return 0, nil
	}
	fee, ok, err := bytesField(authInfo, authInfoFeeField)
	if err != nil {
		return 0, fmt.Errorf("auth_info: %w", err)
	}
	if !ok {
		return 0, nil
	}
	limit, _, err := varintField(fee, feeGasLimitField)
	if err != nil {
		return 0, fmt.Errorf("fee: %w", err)
	}
	return limit, nil
}

// varintField returns the value of the first varint field num in msg, and whether it is there.
func varintField(msg []byte, want protowire.Number) (uint64, bool, error) {
	var last uint64
	found := false
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return 0, false, protowire.ParseError(n)
		}
		msg = msg[n:]
		if num == want && typ == protowire.VarintType {
			v, vn := protowire.ConsumeVarint(msg)
			if vn < 0 {
				return 0, false, protowire.ParseError(vn)
			}
			// Protobuf: a scalar that appears twice takes its last value, as the chain decodes it.
			last, found = v, true
			msg = msg[vn:]
			continue
		}
		skip := protowire.ConsumeFieldValue(num, typ, msg)
		if skip < 0 {
			return 0, false, protowire.ParseError(skip)
		}
		msg = msg[skip:]
	}
	return last, found, nil
}

// bytesField returns the value of the first length-delimited field num in msg.
func bytesField(msg []byte, want protowire.Number) ([]byte, bool, error) {
	var merged []byte
	found := false
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return nil, false, protowire.ParseError(n)
		}
		msg = msg[n:]
		if num == want && typ == protowire.BytesType {
			v, vn := protowire.ConsumeBytes(msg)
			if vn < 0 {
				return nil, false, protowire.ParseError(vn)
			}
			// Protobuf: an embedded message that appears twice is merged, and
			// concatenating the serialized occurrences is that merge.
			if !found {
				merged = v
			} else {
				merged = append(append([]byte(nil), merged...), v...)
			}
			found = true
			msg = msg[vn:]
			continue
		}
		skip := protowire.ConsumeFieldValue(num, typ, msg)
		if skip < 0 {
			return nil, false, protowire.ParseError(skip)
		}
		msg = msg[skip:]
	}
	return merged, found, nil
}

// baseFee reads x/fees' base fee, in norama per unit of gas.
func (p *Proxy) baseFee(ctx context.Context) (*big.Int, error) {
	m, err := chainread.Lookup(baseFeeQuery)
	if err != nil {
		return nil, err
	}
	res, rpcErr, err := p.abciQueryPost(ctx, "/"+baseFeeQuery, nil)
	if err != nil {
		return nil, err
	}
	if rpcErr != nil || res.Code != 0 {
		return nil, fmt.Errorf("base fee query refused (code %d)", res.Code)
	}
	out, err := m.DecodeResponse(res.Value)
	if err != nil {
		return nil, err
	}
	var doc struct {
		BaseFee string `json:"base_fee"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("base fee answer: %w", err)
	}
	fee, ok := new(big.Int).SetString(doc.BaseFee, 10)
	if !ok || fee.Sign() < 0 {
		return nil, fmt.Errorf("base fee %q is not a non-negative integer", doc.BaseFee)
	}
	return fee, nil
}

// abciResult is the response member of an abci_query answer.
type abciResult struct {
	Code      uint32
	Codespace string
	Log       string
	Value     []byte
}

// abciQueryPost runs one abci_query as a JSON-RPC POST, which a transaction needs: its bytes do not
// fit a GET's URL. The node's own RPC error, if it gave one, is returned apart from a transport
// failure.
func (p *Proxy) abciQueryPost(ctx context.Context, path string, data []byte) (abciResult, *rpcError, error) {
	result, rpcErr, err := p.rpcPost(ctx, rpcMethodABCIQuery, map[string]any{
		"path": path, "data": hex.EncodeToString(data), "prove": false,
	})
	if err != nil || rpcErr != nil {
		return abciResult{}, rpcErr, err
	}
	var res struct {
		Response struct {
			Code      uint32 `json:"code"`
			Codespace string `json:"codespace"`
			Log       string `json:"log"`
			Value     string `json:"value"`
		} `json:"response"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		return abciResult{}, nil, fmt.Errorf("abci_query result: %w", err)
	}
	value, err := base64.StdEncoding.DecodeString(res.Response.Value)
	if err != nil {
		return abciResult{}, nil, fmt.Errorf("abci_query value is not base64: %w", err)
	}
	return abciResult{Code: res.Response.Code, Codespace: res.Response.Codespace, Log: res.Response.Log, Value: value}, nil, nil
}

// rpcPost calls one CometBFT JSON-RPC method. It returns the result, or the node's RPC error when
// the answer has one; err is a transport failure or an answer that is not JSON-RPC.
func (p *Proxy) rpcPost(ctx context.Context, method string, params any) (json.RawMessage, *rpcError, error) {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, nil, fmt.Errorf("encode %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.rpc.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, txMaxResponse+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: read answer: %w", method, err)
	}
	if len(body) > txMaxResponse {
		return nil, nil, fmt.Errorf("%s: answer is over %d bytes", method, txMaxResponse)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, nil, fmt.Errorf("%s: answer (HTTP %d) is not JSON-RPC: %w", method, resp.StatusCode, err)
	}
	if env.Error != nil {
		return nil, env.Error, nil
	}
	if len(env.Result) == 0 {
		return nil, nil, fmt.Errorf("%s: answer (HTTP %d) has no result", method, resp.StatusCode)
	}
	return env.Result, nil, nil
}
