package chainread

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// lightStub is a CometBFT RPC that records the JSON-RPC calls it gets and answers each with result,
// or with rpcErr when it is set.
type lightStub struct {
	mu     sync.Mutex
	calls  []lightRequest
	result string
	rpcErr string
}

func (s *lightStub) serve(w http.ResponseWriter, r *http.Request) {
	var req lightRequest
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if s.rpcErr != "" {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"Internal error","data":`+s.rpcErr+`}}`)
		return
	}
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+s.result+`}`)
}

func lightProxy(t *testing.T, s *lightStub) *Proxy {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	return mustProxy(t, srv.URL, srv.URL)
}

func postLight(p *Proxy, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, mountPrefix+lightPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	return rr
}

func decodeLight(t *testing.T, rr *httptest.ResponseRecorder) lightAnswer {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var a lightAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer is not JSON-RPC: %v: %s", err, rr.Body)
	}
	return a
}

// A joining node's light client sends commit and validators with heights as strings (CometBFT's
// encoding of int64) and checks that the answer carries its own id: both must hold through the route.
func TestServeLight_forwardsAllowedMethodsWithTheCallersID(t *testing.T) {
	s := &lightStub{result: `{"signed_header":{"header":{"height":"5"}}}`}
	p := lightProxy(t, s)
	cases := []struct {
		body, method string
		params       map[string]string
		id           string
	}{
		{`{"jsonrpc":"2.0","id":7,"method":"commit","params":{"height":"5"}}`, "commit", map[string]string{"height": `"5"`}, "7"},
		{`{"jsonrpc":"2.0","id":"a1","method":"validators","params":{"height":"5","page":"1","per_page":"100"}}`, "validators",
			map[string]string{"height": `"5"`, "page": `"1"`, "per_page": `"100"`}, `"a1"`},
		{`{"jsonrpc":"2.0","id":8,"method":"consensus_params","params":{"height":5}}`, "consensus_params", map[string]string{"height": `"5"`}, "8"},
		{`{"jsonrpc":"2.0","id":9,"method":"status","params":{}}`, "status", map[string]string{}, "9"},
		{`{"jsonrpc":"2.0","id":10,"method":"commit"}`, "commit", map[string]string{}, "10"},
	}
	for i, tc := range cases {
		a := decodeLight(t, postLight(p, tc.body))
		if a.Error != nil || string(a.ID) != tc.id || string(a.Result) != s.result {
			t.Fatalf("%s: answer %+v", tc.method, a)
		}
		got := s.calls[i]
		if got.Method != tc.method || len(got.Params) != len(tc.params) {
			t.Fatalf("%s: upstream got %+v", tc.method, got)
		}
		for k, v := range tc.params {
			if string(got.Params[k]) != v {
				t.Fatalf("%s: upstream param %s = %s, want %s", tc.method, k, got.Params[k], v)
			}
		}
	}
}

// broadcast_evidence (which the light provider sends when it sees a fork), broadcast_tx and the
// unsafe methods are never forwarded, and neither is a parameter the method does not take.
func TestServeLight_refusesEverythingElseWithoutCallingTheNode(t *testing.T) {
	s := &lightStub{result: `{}`}
	p := lightProxy(t, s)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"broadcast_evidence","params":{"evidence":"x"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":{"tx":"AA=="}}`,
		`{"jsonrpc":"2.0","id":1,"method":"dial_peers","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"commit","params":{"height":"5","prove":true}}`,
		`{"jsonrpc":"2.0","id":1,"method":"validators","params":{"per_page":"101"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"commit","params":{"height":"0"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"commit","params":{"height":"-3"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"commit","params":{"height":null}}`,
		`{"jsonrpc":"1.0","id":1,"method":"status"}`,
	} {
		a := decodeLight(t, postLight(p, body))
		if a.Error == nil || string(a.ID) != "1" {
			t.Fatalf("%s: answered %+v", body, a)
		}
	}
	if len(s.calls) != 0 {
		t.Fatalf("refused requests reached the node: %+v", s.calls)
	}
}

// Malformed requests are refused at the HTTP layer: wrong verb, a batch, an id the answer could not
// safely repeat, extra fields, trailing data, or a body over the bound.
func TestServeLight_malformedRequestsAreRefused(t *testing.T) {
	p := lightProxy(t, &lightStub{result: `{}`})
	get := httptest.NewRequest(http.MethodGet, mountPrefix+lightPath, nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, get)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET: %d %v", rr.Code, rr.Header())
	}
	for body, want := range map[string]int{
		`[{"jsonrpc":"2.0","id":1,"method":"status"}]`:                               http.StatusBadRequest,
		`{"jsonrpc":"2.0","id":{"x":1},"method":"status"}`:                           http.StatusBadRequest,
		`{"jsonrpc":"2.0","method":"status"}`:                                        http.StatusBadRequest,
		`{"jsonrpc":"2.0","id":"` + strings.Repeat("x", 70) + `","method":"status"}`: http.StatusBadRequest,
		`{"jsonrpc":"2.0","id":1,"method":"status","extra":1}`:                       http.StatusBadRequest,
		`{"jsonrpc":"2.0","id":1,"method":"status"}{"x":1}`:                          http.StatusBadRequest,
		``: http.StatusBadRequest,
		`{"jsonrpc":"2.0","id":1,"method":"status","params":{"pad":"` + strings.Repeat("x", lightMaxRequest) + `"}}`: http.StatusRequestEntityTooLarge,
	} {
		if rr := postLight(p, body); rr.Code != want {
			t.Errorf("%.60s: status %d, want %d", body, rr.Code, want)
		}
	}
}

// The node's error ("height 9 is not available") reaches the light client, which decides on it,
// without the paths and addresses a node's message can carry.
func TestServeLight_nodeErrorsAreSanitized(t *testing.T) {
	p := lightProxy(t, &lightStub{rpcErr: `"height 900 must be less than or equal to the current blockchain height 50; /var/lib/orama-global/chain at 10.0.0.3:31001"`})
	a := decodeLight(t, postLight(p, `{"jsonrpc":"2.0","id":3,"method":"commit","params":{"height":"900"}}`))
	if a.Error == nil || !strings.Contains(a.Error.Data, "must be less than or equal to the current blockchain height") {
		t.Fatalf("answer %+v", a)
	}
	if strings.Contains(a.Error.Data, "/var/lib") || strings.Contains(a.Error.Data, "10.0.0.3") {
		t.Fatalf("the node's path or address leaked: %q", a.Error.Data)
	}
}

// When every slot is taken a further call is turned away with Retry-After, not queued.
func TestServeLight_busyGatewayAnswers503(t *testing.T) {
	p := lightProxy(t, &lightStub{result: `{}`})
	for i := 0; i < lightMaxConcurrent; i++ {
		p.lightSlots <- struct{}{}
	}
	rr := postLight(p, `{"jsonrpc":"2.0","id":1,"method":"status"}`)
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d headers %v", rr.Code, rr.Header())
	}
}
