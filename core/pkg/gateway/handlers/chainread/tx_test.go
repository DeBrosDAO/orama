package chainread

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// rpcStub is a CometBFT JSON-RPC endpoint that records every call and answers from its fields.
type rpcStub struct {
	mu      sync.Mutex
	methods []string
	params  []map[string]any

	// simulate and baseFee are the abci_query responses for their paths.
	simulate abciResult
	baseFee  string
	// broadcast is the result of broadcast_tx_sync; broadcastErr, when set, is its RPC error.
	broadcast    map[string]any
	broadcastErr map[string]any
}

func (s *rpcStub) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodPost || json.Unmarshal(body, &req) != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.methods = append(s.methods, req.Method)
	s.params = append(s.params, req.Params)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "abci_query":
		a := s.simulate
		if req.Params["path"] == "/orama.fees.v1.Query/BaseFee" {
			a = abciResult{Value: append([]byte{0x0a, byte(len(s.baseFee))}, s.baseFee...)}
		}
		writeRPC(w, map[string]any{"response": map[string]any{
			"code": a.Code, "codespace": a.Codespace, "log": a.Log, "value": base64.StdEncoding.EncodeToString(a.Value),
		}}, nil)
	case "broadcast_tx_sync":
		writeRPC(w, s.broadcast, s.broadcastErr)
	default:
		writeRPC(w, nil, map[string]any{"code": -32601, "message": "Method not found"})
	}
}

func writeRPC(w http.ResponseWriter, result, rpcErr map[string]any) {
	env := map[string]any{"jsonrpc": "2.0", "id": 1}
	if rpcErr != nil {
		env["error"] = rpcErr
	} else {
		env["result"] = result
	}
	_ = json.NewEncoder(w).Encode(env)
}

func (s *rpcStub) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

func txProxy(t *testing.T, s *rpcStub) *Proxy {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	p, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func postTx(p *Proxy, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chain/"+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	return rr
}

func txBody(raw []byte) string {
	return `{"tx_bytes":"` + base64.StdEncoding.EncodeToString(raw) + `"}`
}

// gasInfoWantedField is GasInfo.gas_wanted, which the proxy does not read.
const gasInfoWantedField = 1

// simulateResponse is SimulateResponse{gas_info: GasInfo{gas_wanted, gas_used}}.
func simulateResponse(wanted, used uint64) []byte {
	var info []byte
	info = protowire.AppendTag(info, gasInfoWantedField, protowire.VarintType)
	info = protowire.AppendVarint(info, wanted)
	info = protowire.AppendTag(info, gasUsedField, protowire.VarintType)
	info = protowire.AppendVarint(info, used)
	out := protowire.AppendTag(nil, simulateGasInfoField, protowire.BytesType)
	return protowire.AppendBytes(out, info)
}

var someTx = []byte{0x0a, 0x03, 'a', 'b', 'c'}

// txWithGasLimit is a TxRaw{auth_info_bytes: AuthInfo{fee: Fee{gas_limit}}}.
func txWithGasLimit(limit uint64) []byte {
	fee := protowire.AppendTag(nil, feeGasLimitField, protowire.VarintType)
	fee = protowire.AppendVarint(fee, limit)
	authInfo := protowire.AppendTag(nil, authInfoFeeField, protowire.BytesType)
	authInfo = protowire.AppendBytes(authInfo, fee)
	raw := protowire.AppendTag(nil, txRawAuthInfoField, protowire.BytesType)
	return protowire.AppendBytes(raw, authInfo)
}

func TestSimulate_answersGasAndFeeFromTheBaseFee(t *testing.T) {
	// The SDK's simulation meters with no limit, so its gas_wanted is the largest uint64. The
	// answer's gas_wanted is the limit the transaction declares.
	s := &rpcStub{simulate: abciResult{Value: simulateResponse(math.MaxUint64, 123456)}, baseFee: "2"}
	tx := txWithGasLimit(200000)
	rr := postTx(txProxy(t, s), "simulate", "application/json", txBody(tx))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got struct {
		GasWanted string `json:"gas_wanted"`
		GasUsed   string `json:"gas_used"`
		Fee       struct{ Denom, Amount string }
		BaseFee   string `json:"base_fee"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("gas figures are not JSON strings: %v: %s", err, rr.Body)
	}
	if got.GasWanted != "200000" || got.GasUsed != "123456" || got.Fee.Denom != "norama" || got.Fee.Amount != "246912" || got.BaseFee != "2" {
		t.Fatalf("answer %+v", got)
	}
	want := hex.EncodeToString(simulateRequest(tx))
	if s.params[0]["path"] != "/cosmos.tx.v1beta1.Service/Simulate" || s.params[0]["data"] != want || s.params[0]["prove"] != false {
		t.Fatalf("simulate sent %v, want data %s", s.params[0], want)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("simulate answer is cacheable")
	}
}

func TestSimulate_gasAboveTwoToTheFiftyThreeIsAnExactDecimalString(t *testing.T) {
	s := &rpcStub{simulate: abciResult{Value: simulateResponse(math.MaxUint64, 9007199254740993)}, baseFee: "1"}
	rr := postTx(txProxy(t, s), "simulate", "application/json", txBody(txWithGasLimit(math.MaxUint64)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	for _, want := range []string{`"gas_wanted":"18446744073709551615"`, `"gas_used":"9007199254740993"`, `"amount":"9007199254740993"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("answer lacks %s: %s", want, rr.Body)
		}
	}
}

func TestSimulate_aTransactionWithoutAGasLimitDeclaresZeroNotTheSDKsSentinel(t *testing.T) {
	s := &rpcStub{simulate: abciResult{Value: simulateResponse(math.MaxUint64, 5)}, baseFee: "1"}
	rr := postTx(txProxy(t, s), "simulate", "application/json", txBody(someTx))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"gas_wanted":"0"`) || strings.Contains(rr.Body.String(), "18446744073709551615") {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
}

// numbersIn lists every bare JSON number in body by its path, so a test can say which members are
// numbers at all.
func numbersIn(t *testing.T, body []byte) map[string]string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	out := map[string]string{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case json.Number:
			out[path] = x.String()
		case map[string]any:
			for k, e := range x {
				walk(path+"."+k, e)
			}
		case []any:
			for _, e := range x {
				walk(path+"[]", e)
			}
		}
	}
	walk("", doc)
	return out
}

func TestTxRoutes_noSixtyFourBitIntegerIsABareJSONNumber(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	s := &rpcStub{
		simulate: abciResult{Value: simulateResponse(math.MaxUint64, math.MaxUint64)}, baseFee: "1",
		broadcast: map[string]any{"code": 0, "hash": hash},
	}
	p := txProxy(t, s)
	answers := map[string]*httptest.ResponseRecorder{
		"simulate":  postTx(p, "simulate", "application/json", txBody(txWithGasLimit(math.MaxUint64))),
		"broadcast": postTx(p, "broadcast", "application/json", txBody(someTx)),
	}
	s.broadcast = map[string]any{"code": 11, "codespace": "sdk", "log": "out of gas", "hash": hash}
	answers["broadcast refused"] = postTx(p, "broadcast", "application/json", txBody(someTx))
	s.simulate = abciResult{Code: 6, Codespace: "sdk", Log: "refused"}
	answers["simulate refused"] = postTx(p, "simulate", "application/json", txBody(someTx))
	for name, rr := range answers {
		for path, value := range numbersIn(t, rr.Body.Bytes()) {
			if path != ".code" {
				t.Errorf("%s: %s is the bare number %s; only code (32-bit) may be one: %s", name, path, value, rr.Body)
			}
		}
	}
}

func TestSimulate_refusalIsCodeCodespaceAndASanitisedLog(t *testing.T) {
	log := "insufficient fees; got: 1norama required: 5norama: at /var/lib/orama/app.go:88 from 10.0.0.7:26657"
	s := &rpcStub{simulate: abciResult{Code: 13, Codespace: "sdk", Log: log}}
	rr := postTx(txProxy(t, s), "simulate", "application/json", txBody(someTx))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != float64(13) || got["codespace"] != "sdk" {
		t.Fatalf("refusal %v", got)
	}
	text, _ := got["log"].(string)
	if !strings.Contains(text, "insufficient fees; got: 1norama required: 5norama") {
		t.Errorf("the reason was lost: %q", text)
	}
	for _, leak := range []string{"/var/lib", "app.go", "10.0.0.7", "26657"} {
		if strings.Contains(rr.Body.String(), leak) {
			t.Errorf("the refusal leaks %q: %s", leak, rr.Body)
		}
	}
	if _, has := got["gas_used"]; has {
		t.Errorf("a refusal carries gas: %v", got)
	}
}

func TestSimulate_chainDownIsAFixed502(t *testing.T) {
	p, err := New(Config{RPCURL: "http://127.0.0.1:9", RESTURL: "http://127.0.0.1:9", IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	rr := postTx(p, "simulate", "application/json", txBody(someTx))
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "127.0.0.1") {
		t.Fatalf("status %d body %q", rr.Code, rr.Body)
	}
}

func TestSimulate_noGasInfoIsABadGateway(t *testing.T) {
	s := &rpcStub{simulate: abciResult{Value: nil}, baseFee: "1"}
	if rr := postTx(txProxy(t, s), "simulate", "application/json", txBody(someTx)); rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
}

func TestBroadcast_usesBroadcastTxSyncAndAnswersTheHash(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	s := &rpcStub{broadcast: map[string]any{"code": 0, "codespace": "", "log": "", "hash": hash}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json; charset=utf-8", txBody(someTx))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got broadcastAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != 0 || got.TxHash != strings.ToUpper(hash) {
		t.Fatalf("answer %+v", got)
	}
	if calls := s.calls(); len(calls) != 1 || calls[0] != "broadcast_tx_sync" {
		t.Fatalf("the node was called with %v, want only broadcast_tx_sync", calls)
	}
	if s.params[0]["tx"] != base64.StdEncoding.EncodeToString(someTx) {
		t.Errorf("tx sent %v", s.params[0]["tx"])
	}
}

func TestBroadcast_checkTxRefusalIsA422WithTheHash(t *testing.T) {
	hash := strings.Repeat("0f", 32)
	s := &rpcStub{broadcast: map[string]any{"code": 32, "codespace": "sdk", "log": "account sequence mismatch, expected 7, got 6: incorrect account sequence", "hash": hash}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got broadcastAnswer
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Code != 32 || got.Codespace != "sdk" || !strings.Contains(got.Log, "sequence mismatch, expected 7") || got.TxHash == "" {
		t.Fatalf("answer %+v", got)
	}
}

func TestBroadcast_aTransactionTheMempoolHasSeenIsTheSDKsRefusalWithTheHash(t *testing.T) {
	s := &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "tx already exists in cache"}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	sum := sha256.Sum256(someTx)
	var got broadcastAnswer
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if rr.Code != http.StatusUnprocessableEntity || got.Code != 19 || got.Codespace != "sdk" || got.TxHash != strings.ToUpper(hex.EncodeToString(sum[:])) {
		t.Fatalf("status %d answer %+v", rr.Code, got)
	}
}

func TestBroadcast_aFullMempoolIsA503ToRetryAndOtherNodeErrorsAreFixed(t *testing.T) {
	s := &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "mempool is full: number of txs 5000"}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d retry-after %q", rr.Code, rr.Header().Get("Retry-After"))
	}
	s = &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "open /var/lib/orama/chain/data: EOF"}}
	rr = postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "/var/lib") {
		t.Fatalf("status %d body %q", rr.Code, rr.Body)
	}
}

func TestBroadcast_aHashThatIsNotAHashIsABadGateway(t *testing.T) {
	s := &rpcStub{broadcast: map[string]any{"code": 0, "hash": "not-a-hash"}}
	if rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx)); rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
}

func TestTxRoutes_refuseABadRequestBeforeTheChainIsAsked(t *testing.T) {
	s := &rpcStub{}
	p := txProxy(t, s)
	huge := make([]byte, txMaxBytes+1)
	cases := []struct {
		name, contentType, body string
		want                    int
	}{
		{"no content type", "", txBody(someTx), http.StatusUnsupportedMediaType},
		{"form content type", "application/x-www-form-urlencoded", txBody(someTx), http.StatusUnsupportedMediaType},
		{"not json", "application/json", "tx", http.StatusBadRequest},
		{"empty body", "application/json", "", http.StatusBadRequest},
		{"missing tx_bytes", "application/json", `{}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"tx_bytes":"YWJj","mode":"commit"}`, http.StatusBadRequest},
		{"second object", "application/json", txBody(someTx) + txBody(someTx), http.StatusBadRequest},
		{"not base64", "application/json", `{"tx_bytes":"***"}`, http.StatusBadRequest},
		{"empty tx", "application/json", `{"tx_bytes":""}`, http.StatusBadRequest},
		{"tx_bytes is a number", "application/json", `{"tx_bytes":12}`, http.StatusBadRequest},
		{"a tx over the mempool limit", "application/json", txBody(huge), http.StatusRequestEntityTooLarge},
		{"a body over the cap", "application/json", `{"tx_bytes":"` + strings.Repeat("A", txMaxBody) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, path := range []string{"simulate", "broadcast"} {
		for _, tc := range cases {
			if rr := postTx(p, path, tc.contentType, tc.body); rr.Code != tc.want {
				t.Errorf("%s %s: status %d, want %d (%q)", path, tc.name, rr.Code, tc.want, rr.Body.String())
			}
		}
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, httptest.NewRequest(method, "/v1/chain/"+path, nil))
			if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
				t.Errorf("%s %s: status %d allow %q", method, path, rr.Code, rr.Header().Get("Allow"))
			}
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chain/"+path+"?mode=commit", strings.NewReader(txBody(someTx)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s with a query: status %d", path, rr.Code)
		}
	}
	if calls := s.calls(); len(calls) != 0 {
		t.Fatalf("a refused request reached the chain: %v", calls)
	}
}

func TestTxRoutes_atTheLimitATransactionIsTaken(t *testing.T) {
	s := &rpcStub{broadcast: map[string]any{"code": 0, "hash": strings.Repeat("11", 32)}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(make([]byte, txMaxBytes)))
	if rr.Code != http.StatusOK {
		t.Fatalf("a transaction of exactly txMaxBytes: status %d: %.200s", rr.Code, rr.Body)
	}
}

func TestTxRoutes_eachRouteHasItsOwnInFlightCap(t *testing.T) {
	s := &rpcStub{simulate: abciResult{Value: simulateResponse(1, 1)}, baseFee: "1", broadcast: map[string]any{"code": 0, "hash": strings.Repeat("22", 32)}}
	p := txProxy(t, s)
	for i := 0; i < simulateMaxConcurrent; i++ {
		p.simulateSlots <- struct{}{}
	}
	rr := postTx(p, "simulate", "application/json", txBody(someTx))
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("a full simulate cap: status %d retry-after %q", rr.Code, rr.Header().Get("Retry-After"))
	}
	if rr := postTx(p, "broadcast", "application/json", txBody(someTx)); rr.Code != http.StatusOK {
		t.Fatalf("broadcast shares simulate's cap: status %d", rr.Code)
	}
	for i := 0; i < broadcastMaxConcurrent; i++ {
		p.broadcastSlots <- struct{}{}
	}
	if rr := postTx(p, "broadcast", "application/json", txBody(someTx)); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("a full broadcast cap: status %d", rr.Code)
	}
	if len(p.querySlots) != 0 {
		t.Errorf("the transaction routes took the module-query slots")
	}
}

func TestParseGasUsed_edges(t *testing.T) {
	if _, err := parseGasUsed(nil); err == nil {
		t.Error("an empty response has gas")
	}
	if _, err := parseGasUsed([]byte{0x0a, 0x05, 0x08}); err == nil {
		t.Error("a truncated gas_info parsed")
	}
	// An unknown field before and inside gas_info is skipped.
	info := protowire.AppendTag(nil, 9, protowire.BytesType)
	info = protowire.AppendBytes(info, []byte("x"))
	info = protowire.AppendTag(info, gasUsedField, protowire.VarintType)
	info = protowire.AppendVarint(info, 7)
	resp := protowire.AppendTag(nil, 3, protowire.VarintType)
	resp = protowire.AppendVarint(resp, 1)
	resp = protowire.AppendTag(resp, simulateGasInfoField, protowire.BytesType)
	resp = protowire.AppendBytes(resp, info)
	used, err := parseGasUsed(resp)
	if err != nil || used != 7 {
		t.Fatalf("used %d err %v", used, err)
	}
}

func TestDeclaredGasLimit_edges(t *testing.T) {
	if got, err := declaredGasLimit(txWithGasLimit(250000)); err != nil || got != 250000 {
		t.Fatalf("limit %d err %v", got, err)
	}
	for name, raw := range map[string][]byte{"no auth_info": someTx, "empty": nil} {
		if got, err := declaredGasLimit(raw); err != nil || got != 0 {
			t.Errorf("%s: limit %d err %v, want 0", name, got, err)
		}
	}
	// An auth_info with no fee declares 0; a fee with no gas_limit declares 0.
	noFee := protowire.AppendBytes(protowire.AppendTag(nil, txRawAuthInfoField, protowire.BytesType), []byte{0x08, 0x01})
	if got, err := declaredGasLimit(noFee); err != nil || got != 0 {
		t.Errorf("no fee: limit %d err %v", got, err)
	}
	noLimit := protowire.AppendBytes(protowire.AppendTag(nil, authInfoFeeField, protowire.BytesType), []byte("\x1a\x01x"))
	noLimitTx := protowire.AppendBytes(protowire.AppendTag(nil, txRawAuthInfoField, protowire.BytesType), noLimit)
	if got, err := declaredGasLimit(noLimitTx); err != nil || got != 0 {
		t.Errorf("no gas_limit: limit %d err %v", got, err)
	}
	// Unknown fields around the limit are skipped; a truncated message is an error.
	if _, err := declaredGasLimit([]byte{0x12, 0x05, 0x12}); err == nil {
		t.Error("a truncated auth_info parsed")
	}
}

func TestSanitizeLog_table(t *testing.T) {
	cases := []struct{ name, in, notWant string }{
		{"source location", "panic at x/fees/ante/fee.go:120", "fee.go"},
		{"filesystem path", "open /home/orama/.oramad/data/application.db: no such file", "/home/orama"},
		{"ipv4 with port", "dial tcp 10.0.0.3:26657: connection refused", "10.0.0.3"},
		{"ipv6", "dial tcp [fd00::1]:26657: refused", "fd00::1"},
		{"stack trace", "recovered: boom\ngoroutine 55 [running]:\nmain.run()", "goroutine"},
		{"control characters", "bad\x00\x1b[31m tx", "\x1b"},
	}
	for _, tc := range cases {
		if got := sanitizeLog(tc.in); strings.Contains(got, tc.notWant) {
			t.Errorf("%s: %q still contains %q", tc.name, got, tc.notWant)
		}
	}
	reason := "insufficient funds: 5norama is smaller than 10norama: insufficient funds"
	if got := sanitizeLog(reason); got != reason {
		t.Errorf("a plain reason was changed: %q", got)
	}
	if got := sanitizeLog(strings.Repeat("é", logMaxRunes*2)); len([]rune(got)) != logMaxRunes {
		t.Errorf("log is %d runes, want %d", len([]rune(got)), logMaxRunes)
	}
	if got := sanitizeLog("bad \xff utf8"); strings.ContainsRune(got, '�') || strings.Contains(got, "\xff") {
		t.Errorf("invalid UTF-8 survived: %q", got)
	}
	if got := sanitizeLog(""); got != "" {
		t.Errorf("empty log became %q", got)
	}
}

func TestSanitizeCodespace(t *testing.T) {
	for in, want := range map[string]string{"sdk": "sdk", "wasm": "wasm", "": "", "a b": "", "/etc/passwd": "", strings.Repeat("a", 65): ""} {
		if got := sanitizeCodespace(in); got != want {
			t.Errorf("sanitizeCodespace(%q) = %q, want %q", in, got, want)
		}
	}
}

// A field that appears twice decodes as the chain decodes it: a repeated
// gas_limit takes its last value, and a repeated fee (or auth_info) message is
// merged, so a limit in the second copy wins. Taking the first occurrence showed
// a wallet a limit the chain would not apply.
func TestDeclaredGasLimit_duplicateFieldsDecodeAsTheChainDoes(t *testing.T) {
	limit := func(v uint64) []byte {
		return protowire.AppendVarint(protowire.AppendTag(nil, feeGasLimitField, protowire.VarintType), v)
	}
	feeMsg := func(b []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, authInfoFeeField, protowire.BytesType), b)
	}
	authInfo := func(b []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, txRawAuthInfoField, protowire.BytesType), b)
	}
	for name, tc := range map[string]struct {
		raw  []byte
		want uint64
	}{
		"gas_limit twice in one fee":     {authInfo(feeMsg(append(limit(100), limit(200)...))), 200},
		"fee twice, limit in the second": {authInfo(append(feeMsg(limit(100)), feeMsg(limit(300))...)), 300},
		"fee twice, limit in the first":  {authInfo(append(feeMsg(limit(100)), feeMsg(nil)...)), 100},
		"auth_info twice":                {append(authInfo(feeMsg(limit(100))), authInfo(feeMsg(limit(400)))...), 400},
	} {
		got, err := declaredGasLimit(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("%s: limit %d err %v, want %d", name, got, err, tc.want)
		}
	}
}
