package statesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const (
	testChain  = "orama-stagenet-6"
	testIDA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testIDB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testHash   = "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	otherHash  = "0000000000000000000000000000000000000000000000000000000000000001"
	testHeight = 5000
)

// seed is a fake gateway answering the light route.
type seed struct {
	nodeID, network, hash string
	height                int64
	catchingUp            bool
	failCommit            bool
	asked                 []string
}

func (s *seed) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != LightPath {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request: %v", err)
		}
		s.asked = append(s.asked, req.Method+" "+req.Params["height"])
		switch req.Method {
		case "status":
			writeResult(w, map[string]any{
				"node_info": map[string]any{"id": s.nodeID, "network": s.network},
				"sync_info": map[string]any{"latest_block_height": strconv.FormatInt(s.height, 10), "catching_up": s.catchingUp},
			})
		case "commit":
			if s.failCommit {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32603, "message": "height is not available"}})
				return
			}
			writeResult(w, map[string]any{"signed_header": map[string]any{
				"header": map[string]any{"height": req.Params["height"]},
				"commit": map[string]any{"block_id": map[string]any{"hash": s.hash}},
			}})
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	})
}

func writeResult(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
}

// start serves s over TLS and returns its host:port and a client that trusts it.
func start(t *testing.T, s *seed) (string, Doer) {
	t.Helper()
	srv := httptest.NewTLSServer(s.handler(t))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), srv.Client()
}

func twoSeeds(t *testing.T, a, b *seed) ([]string, Doer) {
	t.Helper()
	hostA, client := start(t, a)
	hostB, _ := start(t, b)
	return []string{hostA, hostB}, client
}

func goodSeed(id string) *seed {
	return &seed{nodeID: id, network: testChain, hash: testHash, height: testHeight}
}

func TestResolve_twoAgreeingSeeds(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	b.height = testHeight + 7
	seeds, hc := twoSeeds(t, a, b)
	tp, err := Resolve(context.Background(), seeds, testChain, hc)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(testHeight - TrustHeightMargin); tp.Height != want {
		t.Errorf("height %d, want %d: the lowest newest block less the margin", tp.Height, want)
	}
	if tp.Hash != strings.ToLower(testHash) {
		t.Errorf("hash %q", tp.Hash)
	}
	if len(tp.Servers) != 2 || !strings.HasSuffix(tp.Servers[0], LightPath) {
		t.Errorf("servers %v", tp.Servers)
	}
	wantPeers := testIDA + "@" + seeds[0] + ":31000," + testIDB + "@" + seeds[1] + ":31000"
	if got := tp.PersistentPeers(); got != wantPeers {
		t.Errorf("persistent peers %q, want %q", got, wantPeers)
	}
	for _, s := range []*seed{a, b} {
		if want := "commit " + strconv.Itoa(testHeight-TrustHeightMargin); s.asked[len(s.asked)-1] != want {
			t.Errorf("a seed was asked %v, want its last question to be %q", s.asked, want)
		}
	}
}

func TestResolve_seedsThatDisagreeAreRefused(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	b.hash = otherHash
	seeds, hc := twoSeeds(t, a, b)
	_, err := Resolve(context.Background(), seeds, testChain, hc)
	if err == nil || !strings.Contains(err.Error(), "refusing to pick one") {
		t.Fatalf("got %v, want a refusal to pick between two blocks", err)
	}
}

func TestResolve_aSeedOnAnotherChainIsRefused(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	b.network = "orama-stagenet-5"
	seeds, hc := twoSeeds(t, a, b)
	_, err := Resolve(context.Background(), seeds, testChain, hc)
	if err == nil || !strings.Contains(err.Error(), "orama-stagenet-5") {
		t.Fatalf("got %v, want the wrong chain named", err)
	}
}

func TestResolve_aSeedStillCatchingUp(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	a.catchingUp = true
	seeds, hc := twoSeeds(t, a, b)
	if _, err := Resolve(context.Background(), seeds, testChain, hc); err == nil || !strings.Contains(err.Error(), "catching up") {
		t.Fatalf("got %v", err)
	}
}

func TestResolve_oneSeedIsNotEnough(t *testing.T) {
	if _, err := Resolve(context.Background(), []string{"seed1.example.org"}, testChain, http.DefaultClient); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("got %v", err)
	}
}

func TestResolve_aYoungChainHasNoTrustPoint(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	a.height, b.height = 50, 60
	seeds, hc := twoSeeds(t, a, b)
	if _, err := Resolve(context.Background(), seeds, testChain, hc); err == nil || !strings.Contains(err.Error(), "below") {
		t.Fatalf("got %v", err)
	}
}

func TestResolve_aCommitTheSeedCannotServe(t *testing.T) {
	a, b := goodSeed(testIDA), goodSeed(testIDB)
	b.failCommit = true
	seeds, hc := twoSeeds(t, a, b)
	if _, err := Resolve(context.Background(), seeds, testChain, hc); err == nil || !strings.Contains(err.Error(), "height is not available") {
		t.Fatalf("got %v, want the seed's own reason", err)
	}
}

func TestResolve_badNodeIDOrHashFromASeed(t *testing.T) {
	for name, mutate := range map[string]func(*seed){
		"node id not hex":    func(s *seed) { s.nodeID = "not-a-node-id" },
		"uppercase node id":  func(s *seed) { s.nodeID = strings.ToUpper(testIDA) },
		"a short block hash": func(s *seed) { s.hash = "abcd" },
		"a non-hex hash":     func(s *seed) { s.hash = strings.Repeat("zz", 32) },
		"height zero latest": func(s *seed) { s.height = 0 },
	} {
		a, b := goodSeed(testIDA), goodSeed(testIDB)
		mutate(b)
		seeds, hc := twoSeeds(t, a, b)
		if _, err := Resolve(context.Background(), seeds, testChain, hc); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestClient_refusesANonHTTPSEndpoint(t *testing.T) {
	c := Client{BaseURL: "http://seed.example.org", HTTP: http.DefaultClient}
	if _, err := c.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("got %v", err)
	}
}

func TestClient_aGatewayAnswering500(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("got %v", err)
	}
}

func TestNewHTTPClient_refusesARedirectToHTTP(t *testing.T) {
	c := NewHTTPClient()
	req, _ := http.NewRequest(http.MethodGet, "http://example.org/", nil)
	first, _ := http.NewRequest(http.MethodPost, "https://example.org/v1/chain/light", nil)
	if err := c.CheckRedirect(req, []*http.Request{first}); err == nil {
		t.Fatal("a redirect to http must be refused")
	}
	bounded := make([]*http.Request, maxRedirects)
	for i := range bounded {
		bounded[i] = first
	}
	same, _ := http.NewRequest(http.MethodPost, "https://example.org/other", nil)
	if err := c.CheckRedirect(same, bounded); err == nil {
		t.Fatal("redirects must be bounded")
	}
}

func TestNewHTTPClient_followsOnlyARedirectOnTheSameHost(t *testing.T) {
	c := NewHTTPClient()
	first, _ := http.NewRequest(http.MethodPost, "https://seed1.example.org/v1/chain/light", nil)
	same, _ := http.NewRequest(http.MethodPost, "https://seed1.example.org/v1/chain/light/", nil)
	other, _ := http.NewRequest(http.MethodPost, "https://seed2.example.org/v1/chain/light", nil)
	if err := c.CheckRedirect(same, []*http.Request{first}); err != nil {
		t.Errorf("a redirect on the same host is followed: %v", err)
	}
	if err := c.CheckRedirect(other, []*http.Request{first}); err == nil || !strings.Contains(err.Error(), "answers for itself") {
		t.Errorf("a redirect to another host is a different witness: %v", err)
	}
}
