package chainread

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// abciUpstream answers abci_query with value (or a chain error) and records what it was asked.
type abciUpstream struct {
	mu    sync.Mutex
	calls []url.Values
	paths []string
	code  uint32
	log   string
	value []byte
	// latest is the height /status reports; zero means 1000.
	latest int64
}

func (u *abciUpstream) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/status" {
		latest := u.latest
		if latest == 0 {
			latest = 1000
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"sync_info":{"latest_block_height":"` + strconv.FormatInt(latest, 10) + `"}}}`))
		return
	}
	u.mu.Lock()
	u.calls = append(u.calls, r.URL.Query())
	u.paths = append(u.paths, r.URL.Path)
	u.mu.Unlock()
	out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": -1, "result": map[string]any{
		"response": map[string]any{
			"code": u.code, "codespace": "sdk", "log": u.log, "value": base64.StdEncoding.EncodeToString(u.value),
		},
	}})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func queryProxy(t *testing.T, u *abciUpstream) *Proxy {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(u.handler))
	t.Cleanup(srv.Close)
	p, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func getQuery(p *Proxy, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	return rr
}

const nodeQuery = "/v1/chain/query/orama.nodes.v1.Query/Node"

// nodeAnswer is QueryNodeResponse{node: Node{node_id: "n-1"}}.
var nodeAnswer = []byte{0x0a, 0x05, 0x0a, 0x03, 'n', '-', '1'}

func TestQuery_dataFormReachesAbciQueryAndDecodesTheAnswer(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer}
	p := queryProxy(t, up)
	data := base64.StdEncoding.EncodeToString([]byte{0x0a, 0x03, 'n', '-', '1'})
	rr := getQuery(p, http.MethodGet, nodeQuery+"?data="+url.QueryEscape(data)+"&height=1000")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Node struct {
			NodeID string `json:"node_id"`
		} `json:"node"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Node.NodeID != "n-1" {
		t.Fatalf("body %q err %v", rr.Body.String(), err)
	}
	if len(up.calls) != 1 || up.paths[0] != "/abci_query" {
		t.Fatalf("upstream calls %v %v", up.paths, up.calls)
	}
	q := up.calls[0]
	if q.Get("path") != `"/orama.nodes.v1.Query/Node"` || q.Get("data") != "0x0a036e2d31" ||
		q.Get("height") != "1000" || q.Get("prove") != "false" {
		t.Fatalf("upstream query %v", q)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("answers must not be cached")
	}
}

func TestQuery_jsonFormAndURLSafeBase64AndEmptyRequest(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer}
	p := queryProxy(t, up)
	if rr := getQuery(p, http.MethodGet, nodeQuery+"?json="+url.QueryEscape(`{"node_id":"n-1"}`)); rr.Code != http.StatusOK {
		t.Fatalf("json form: %d %q", rr.Code, rr.Body.String())
	}
	if up.calls[0].Get("data") != "0x0a036e2d31" {
		t.Fatalf("json request became %q", up.calls[0].Get("data"))
	}
	urlSafe := base64.RawURLEncoding.EncodeToString([]byte{0x0a, 0x03, 'n', '-', '1'})
	if rr := getQuery(p, http.MethodGet, nodeQuery+"?data="+urlSafe); rr.Code != http.StatusOK {
		t.Fatalf("url-safe form: %d", rr.Code)
	}
	getQuery(p, http.MethodGet, "/v1/chain/query/orama.nodes.v1.Query/Params")
	last := up.calls[len(up.calls)-1]
	if last.Get("path") != `"/orama.nodes.v1.Query/Params"` || last.Has("data") {
		t.Fatalf("an empty request sent %v", last)
	}
}

func TestQuery_aMissingKeyIs404AndOtherChainErrorsAre502(t *testing.T) {
	up := &abciUpstream{code: 22, log: "internal store path /data/x"}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, nodeQuery+"?json="+url.QueryEscape(`{"node_id":"x"}`))
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "/data/x") {
		t.Fatalf("not found: %d %q", rr.Code, rr.Body.String())
	}
	up.code, up.log = 5, "secret detail"
	rr = getQuery(p, http.MethodGet, nodeQuery)
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("chain error: %d %q", rr.Code, rr.Body.String())
	}
}

func TestQuery_refusesEverythingThatIsNotAnEmbeddedQuery(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer}
	p := queryProxy(t, up)
	good := base64.RawURLEncoding.EncodeToString([]byte{0x0a, 0x03, 'n', '-', '1'})
	cases := []struct {
		name, method, target string
		want                 int
	}{
		{"module invariants", http.MethodGet, "/v1/chain/query/orama.storage.v1.Query/Invariants", 404},
		{"every invariants", http.MethodGet, "/v1/chain/query/orama.fees.v1.Query/Invariants", 404},
		{"houses tiers", http.MethodGet, "/v1/chain/query/orama.houses.v1.Query/Tiers", 404},
		{"shielded pools", http.MethodGet, "/v1/chain/query/orama.shielded.v1.Query/Pools", 404},
		{"shielded invariants", http.MethodGet, "/v1/chain/query/orama.shielded.v1.Query/Invariants", 404},
		{"unknown service", http.MethodGet, "/v1/chain/query/orama.nope.v1.Query/Node", 404},
		{"unknown method", http.MethodGet, "/v1/chain/query/orama.nodes.v1.Query/Nope", 404},
		{"msg service", http.MethodGet, "/v1/chain/query/orama.nodes.v1.Msg/RegisterNode", 404},
		{"cosmos method not on the wallet list", http.MethodGet, "/v1/chain/query/cosmos.bank.v1beta1.Query/TotalSupply", 404},
		{"staking walk of every validator", http.MethodGet, "/v1/chain/query/cosmos.staking.v1beta1.Query/Validators", 404},
		{"wasm walk of a contract's state", http.MethodGet, "/v1/chain/query/cosmwasm.wasm.v1.Query/AllContractState", 404},
		{"tx path", http.MethodGet, "/v1/chain/query/cosmos.tx.v1beta1.Service/BroadcastTx", 404},
		{"bare service", http.MethodGet, "/v1/chain/query/orama.nodes.v1.Query", 404},
		{"extra segment", http.MethodGet, nodeQuery + "/extra", 404},
		{"empty name", http.MethodGet, "/v1/chain/query/", 404},
		{"dot dot", http.MethodGet, "/v1/chain/query/../status", 404},
		{"encoded slash", http.MethodGet, "/v1/chain/query/orama.nodes.v1.Query%2FNode", 404},
		{"post", http.MethodPost, nodeQuery, 405},
		{"delete", http.MethodDelete, nodeQuery, 405},
		{"both forms", http.MethodGet, nodeQuery + "?data=" + good + "&json=%7B%7D", 400},
		{"bad base64", http.MethodGet, nodeQuery + "?data=***", 400},
		{"not a proto", http.MethodGet, nodeQuery + "?data=" + base64.RawURLEncoding.EncodeToString([]byte{0xff}), 400},
		{"bad json", http.MethodGet, nodeQuery + "?json=%7Bnope", 400},
		{"unknown field", http.MethodGet, nodeQuery + "?json=" + url.QueryEscape(`{"nope":1}`), 400},
		{"prove", http.MethodGet, nodeQuery + "?prove=true", 400},
		{"path override", http.MethodGet, nodeQuery + "?path=%22%2Ftx%22", 400},
		{"repeated data", http.MethodGet, nodeQuery + "?data=" + good + "&data=" + good, 400},
		{"height zero-padded", http.MethodGet, nodeQuery + "?height=007", 400},
		{"height negative", http.MethodGet, nodeQuery + "?height=-1", 400},
		{"height huge", http.MethodGet, nodeQuery + "?height=99999999999999999999", 400},
		{"height text", http.MethodGet, nodeQuery + "?height=latest", 400},
		{"semicolon", http.MethodGet, nodeQuery + "?data=" + good + ";x=1", 400},
		{"oversized data", http.MethodGet, nodeQuery + "?data=" + strings.Repeat("A", 8<<10), 400},
		{"oversized json", http.MethodGet, nodeQuery + "?json=" + url.QueryEscape(`{"node_id":"`+strings.Repeat("a", 5<<10)+`"}`), 400},
	}
	for _, tc := range cases {
		rr := getQuery(p, tc.method, tc.target)
		if rr.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%q)", tc.name, rr.Code, tc.want, rr.Body.String())
		}
	}
	if len(up.calls) != 0 {
		t.Fatalf("a refused request reached the chain: %v", up.calls)
	}
}

func TestQuery_heightZeroMeansLatestAndIsNotForwarded(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer}
	p := queryProxy(t, up)
	if rr := getQuery(p, http.MethodGet, nodeQuery+"?height=0"); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if _, has := up.calls[0]["height"]; has {
		t.Fatalf("height 0 was forwarded: %v", up.calls[0])
	}
}

func TestQuery_aResponseOverTheCapIsRefusedWhole(t *testing.T) {
	up := &abciUpstream{value: make([]byte, queryMaxResponse)}
	p := queryProxy(t, up)
	if rr := getQuery(p, http.MethodGet, nodeQuery); rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestQuery_onlyQueryServicesAreAllowed(t *testing.T) {
	allowed, err := queryAllowed()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"orama.nodes.v1.Query/Node", "orama.storage.v1.Query/Deal", "orama.fees.v1.Query/Earnings"} {
		if _, ok := allowed[want]; !ok {
			t.Errorf("%s is not served", want)
		}
	}
	for name := range allowed {
		service, _, _ := strings.Cut(name, "/")
		_, wallet := walletQuery[name]
		if !wallet && !strings.HasPrefix(service, "orama.") || !strings.HasSuffix(service, ".Query") {
			t.Errorf("%s is served but is neither an Orama Query service nor on the wallet list", name)
		}
	}
}

// Every embedded Query method is decided: served by publicQuery or refused by withheldQuery. A new
// module query that is in neither fails here, so it cannot become public by being embedded, and
// nothing is on both lists.
func TestQuery_everyEmbeddedMethodIsClassified(t *testing.T) {
	all, err := chainread.Methods()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		service, method, _ := strings.Cut(n, "/")
		if !strings.HasSuffix(service, ".Query") {
			continue
		}
		_, pub := publicQuery[n]
		_, held := withheldQuery[n]
		if pub == held {
			t.Errorf("%s must be on exactly one of publicQuery and withheldQuery (public=%v withheld=%v)", n, pub, held)
		}
		if method == "Invariants" && pub {
			t.Errorf("%s walks a module's whole state and must never be public", n)
		}
	}
	for n := range publicQuery {
		if _, held := withheldQuery[n]; held {
			t.Errorf("%s is both public and withheld", n)
		}
	}
	allowed, err := queryAllowed()
	if err != nil {
		t.Fatal(err)
	}
	for n := range allowed {
		if strings.HasSuffix(n, "/Invariants") || n == "orama.houses.v1.Query/Tiers" {
			t.Errorf("%s is served", n)
		}
	}
}

func TestQuery_heightMustBeWithinTheLastBlocks(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer, latest: 5000}
	p := queryProxy(t, up)
	for height, want := range map[string]int{
		"5000": http.StatusOK,
		"4900": http.StatusOK,
		"4899": http.StatusBadRequest,
		"1":    http.StatusBadRequest,
	} {
		rr := getQuery(p, http.MethodGet, nodeQuery+"?height="+height)
		if rr.Code != want {
			t.Errorf("height %s: status %d, want %d (%q)", height, rr.Code, want, rr.Body.String())
		}
	}
	calls := len(up.calls)
	if rr := getQuery(p, http.MethodGet, nodeQuery+"?height=1"); rr.Code != http.StatusBadRequest || len(up.calls) != calls {
		t.Fatalf("a query outside the window reached the chain: %d, %d calls", rr.Code, len(up.calls)-calls)
	}
	if rr := getQuery(p, http.MethodGet, nodeQuery); rr.Code != http.StatusOK {
		t.Fatalf("a latest-height query needs no window check: %d", rr.Code)
	}
}

func TestQuery_heightWindowFailsClosedWhenTheLatestHeightIsUnreadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"sync_info":{"latest_block_height":"soon"}}}`))
	}))
	t.Cleanup(srv.Close)
	broken, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if rr := getQuery(broken, http.MethodGet, nodeQuery+"?height=5"); rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rr.Code)
	}
}

// Only queryMaxConcurrent module queries run at once; the rest are told to retry.
func TestQuery_concurrencyIsCapped(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, queryMaxConcurrent+4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"response":{"code":0,"value":""}}}`))
	}))
	t.Cleanup(srv.Close)
	p, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < queryMaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getQuery(p, http.MethodGet, nodeQuery)
		}()
	}
	for i := 0; i < queryMaxConcurrent; i++ {
		<-entered
	}
	rr := getQuery(p, http.MethodGet, nodeQuery)
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Errorf("query %d over the cap: status %d retry-after %q", queryMaxConcurrent+1, rr.Code, rr.Header().Get("Retry-After"))
	}
	close(release)
	wg.Wait()
	if rr := getQuery(p, http.MethodGet, nodeQuery); rr.Code == http.StatusServiceUnavailable {
		t.Errorf("the slots were not released: %d", rr.Code)
	}
}
