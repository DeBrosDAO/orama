package chainread

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"strings"
)

// Transaction routes for a wallet, which has no tunnel to a node:
//
//	POST /v1/chain/simulate   {"tx_bytes":"<base64 TxRaw>"}
//	POST /v1/chain/broadcast  {"tx_bytes":"<base64 TxRaw>"}
//
// Simulate runs the transaction through the node's ante handlers and messages without keeping
// anything (abci_query /cosmos.tx.v1beta1.Service/Simulate). Broadcast submits a signed transaction
// with CometBFT's broadcast_tx_sync, which answers after CheckTx and never waits for a block; the
// caller then reads /v1/chain/tx?hash= until it is in one. The fee a transaction pays is checked
// on chain, so a spam transaction costs its sender; the gateway only bounds the load: the body, the
// requests in flight (each route has its own), and, in the gateway's rate limiter, the rate.
const (
	simulatePath  = "simulate"
	broadcastPath = "broadcast"

	// txMaxBytes is the largest transaction either route takes: CometBFT's default mempool
	// max_tx_bytes, which the chain does not change. A larger one is refused before it is sent.
	txMaxBytes = 1 << 20
	// txMaxBody bounds the request body: the base64 of txMaxBytes and the JSON around it.
	txMaxBody = txMaxBytes/3*4 + 4 + 1<<10
	// txMaxResponse bounds an answer to a transaction call; the largest is a CheckTx log.
	txMaxResponse = 1 << 20

	// simulateMaxConcurrent and broadcastMaxConcurrent are the requests each route has in flight
	// at once. The rest are answered 503 with Retry-After.
	simulateMaxConcurrent  = 8
	broadcastMaxConcurrent = 16
	txBusyRetryAfter       = "2"

	rpcMethodABCIQuery = "abci_query"
	// rpcMethodBroadcast is the only way a transaction is submitted. Never broadcast_tx_commit: it
	// holds the request open for a block and the node's RPC timeout.
	rpcMethodBroadcast = "broadcast_tx_sync"

	simulateABCIPath = "/cosmos.tx.v1beta1.Service/Simulate"
	baseFeeQuery     = "orama.fees.v1.Query/BaseFee"
	feeDenom         = "norama"

	// SimulateRequest.tx_bytes, and the fields of SimulateResponse.gas_info and its GasInfo.
	simulateTxBytesField = 2
	simulateGasInfoField = 1
	gasUsedField         = 2

	// The path to the gas limit a transaction declares: TxRaw.auth_info_bytes, AuthInfo.fee, Fee.gas_limit.
	txRawAuthInfoField = 2
	authInfoFeeField   = 2
	feeGasLimitField   = 2

	// sdkTxInCache is the SDK's code and codespace for a transaction the mempool has already seen.
	// CometBFT answers a repeat with an RPC error rather than a result; the SDK's own mempool gives
	// a CheckTx result with this code, which is the shape a wallet already handles.
	sdkTxInCacheCode      = 19
	sdkTxInCacheCodespace = "sdk"
	txInCacheLog          = "tx already in mempool cache"
	txInCacheMessage      = "already exists in cache"
	mempoolFullMessage    = "mempool is full"
)

// txRequest is the body of both routes.
type txRequest struct {
	TxBytes string `json:"tx_bytes"`
}

// txRefusal is what a transaction the chain refused is answered with. A broadcast adds the hash.
type txRefusal struct {
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace"`
	Log       string `json:"log"`
	TxHash    string `json:"tx_hash,omitempty"`
}

// broadcastAnswer is what a broadcast is answered with, accepted or not.
type broadcastAnswer struct {
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace"`
	Log       string `json:"log"`
	TxHash    string `json:"tx_hash"`
}

type feeCoin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// simulateAnswer carries its 64-bit integers as decimal strings, as proto3 JSON does and as every
// other 64-bit integer this proxy answers is: a JavaScript client reads a bare number above 2^53
// as a different one.
type simulateAnswer struct {
	// GasWanted is the gas limit the transaction declares (its fee's gas_limit), so a wallet can see
	// that a limit is under GasUsed. The chain's simulation itself runs with no limit and reports
	// the largest uint64.
	GasWanted uint64  `json:"gas_wanted,string"`
	GasUsed   uint64  `json:"gas_used,string"`
	Fee       feeCoin `json:"fee"`
	// BaseFee is the norama per unit of gas Fee is priced at, so a wallet that pads the gas limit
	// can price the padded limit itself.
	BaseFee string `json:"base_fee"`
}

func (p *Proxy) serveSimulate(w http.ResponseWriter, r *http.Request) {
	raw, ok := readTx(w, r)
	if !ok {
		return
	}
	if !acquire(w, p.simulateSlots) {
		return
	}
	defer release(p.simulateSlots)
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()

	res, rpcErr, err := p.abciQueryPost(ctx, simulateABCIPath, simulateRequest(raw))
	if err != nil || rpcErr != nil {
		writeErr(w, http.StatusBadGateway, "chain unreachable")
		return
	}
	if res.Code != 0 {
		writeJSON(w, http.StatusUnprocessableEntity, refusal(res.Code, res.Codespace, res.Log, ""))
		return
	}
	gasUsed, err := parseGasUsed(res.Value)
	if err != nil {
		writeErr(w, http.StatusBadGateway, msgChainFailure)
		return
	}
	gasWanted, err := declaredGasLimit(raw)
	if err != nil {
		writeErr(w, http.StatusBadGateway, msgChainFailure)
		return
	}
	baseFee, err := p.baseFee(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, msgChainFailure)
		return
	}
	fee := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(gasUsed))
	writeJSON(w, http.StatusOK, simulateAnswer{
		GasWanted: gasWanted, GasUsed: gasUsed,
		Fee:     feeCoin{Denom: feeDenom, Amount: fee.String()},
		BaseFee: baseFee.String(),
	})
}

func (p *Proxy) serveBroadcast(w http.ResponseWriter, r *http.Request) {
	raw, ok := readTx(w, r)
	if !ok {
		return
	}
	if !acquire(w, p.broadcastSlots) {
		return
	}
	defer release(p.broadcastSlots)
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()

	result, rpcErr, err := p.rpcPost(ctx, rpcMethodBroadcast, map[string]any{"tx": base64.StdEncoding.EncodeToString(raw)})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "chain unreachable")
		return
	}
	if rpcErr != nil {
		p.broadcastRPCError(w, *rpcErr, raw)
		return
	}
	var res struct {
		Code      uint32 `json:"code"`
		Codespace string `json:"codespace"`
		Log       string `json:"log"`
		Hash      string `json:"hash"`
	}
	if json.Unmarshal(result, &res) != nil || !hash32Pattern.MatchString(res.Hash) {
		writeErr(w, http.StatusBadGateway, msgChainFailure)
		return
	}
	answer := broadcastAnswer{
		Code: res.Code, Codespace: sanitizeCodespace(res.Codespace), Log: sanitizeLog(res.Log),
		TxHash: strings.ToUpper(res.Hash),
	}
	status := http.StatusOK
	if res.Code != 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, answer)
}

// broadcastRPCError answers a broadcast the node refused before CheckTx: a transaction it has
// already seen is the refusal the SDK gives for it, with the hash to poll; a full mempool is a
// 503 to retry; anything else is a bad gateway.
func (p *Proxy) broadcastRPCError(w http.ResponseWriter, e rpcError, raw []byte) {
	text := strings.ToLower(e.Message + " " + e.Data)
	switch {
	case strings.Contains(text, txInCacheMessage):
		writeJSON(w, http.StatusUnprocessableEntity, refusal(sdkTxInCacheCode, sdkTxInCacheCodespace, txInCacheLog, txHashOf(raw)))
	case strings.Contains(text, mempoolFullMessage):
		w.Header().Set("Retry-After", txBusyRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, "the chain's mempool is full, try again shortly")
	default:
		writeErr(w, http.StatusBadGateway, msgChainFailure)
	}
}

func refusal(code uint32, codespace, log, hash string) txRefusal {
	return txRefusal{Code: code, Codespace: sanitizeCodespace(codespace), Log: sanitizeLog(log), TxHash: hash}
}

// readTx checks a transaction route's request and returns the transaction bytes. It writes the
// refusal itself and reports false for the wrong method or content type, an oversized or malformed
// body, or a transaction that is empty, not base64, or over txMaxBytes.
func readTx(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil, false
	}
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return nil, false
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "send application/json")
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, txMaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "transaction too large")
			return nil, false
		}
		writeErr(w, http.StatusBadRequest, "bad body")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req txRequest
	if err := dec.Decode(&req); err != nil || dec.More() {
		writeErr(w, http.StatusBadRequest, "bad body")
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(req.TxBytes)
	if err != nil || len(raw) == 0 {
		writeErr(w, http.StatusBadRequest, "tx_bytes must be the base64 of a transaction")
		return nil, false
	}
	if len(raw) > txMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "transaction too large")
		return nil, false
	}
	return raw, true
}

func acquire(w http.ResponseWriter, slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", txBusyRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, "too many transactions in flight")
		return false
	}
}

func release(slots chan struct{}) { <-slots }

func writeJSON(w http.ResponseWriter, status int, v any) {
	out, err := json.Marshal(v)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "chain proxy failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(out)
	_, _ = io.WriteString(w, "\n")
}
