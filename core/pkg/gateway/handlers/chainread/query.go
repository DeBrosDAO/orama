package chainread

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// Module queries: GET /v1/chain/query/<package.Service>/<Method> runs one gRPC query of an Orama
// module through the local node's CometBFT abci_query and answers the decoded response as JSON,
// with the proto field names. The gateway reaches x/nodes, x/storage, x/fees and the rest only
// through this route; a node also serves them on its own REST API.
//
// Only the Query services embedded in core/pkg/chainread are served, so a Msg, a transaction or
// any other ABCI path is unreachable. The request is one of
//
//	data=<base64 protobuf request>   (standard or URL-safe alphabet, padding optional)
//	json=<JSON request>              (proto field names, snake_case or lowerCamel)
//
// or neither, for the empty request. height=<n> reads at that height, which must be within the
// last queryMaxHeightAge blocks; absent or 0 is the latest. The query never asks for a proof and
// never writes. At most queryMaxConcurrent run at once, and the node's query-gas-limit bounds each.
const (
	queryPrefix = "query/"

	// queryMaxRequest bounds the request as protobuf bytes and as JSON text.
	queryMaxRequest = 4 << 10
	// queryMaxResponse bounds the upstream answer, as chainread's own reader does.
	queryMaxResponse = 4 << 20

	// queryMaxHeightAge is how far behind the latest block a public query may read. A query at an
	// old height makes the node open an old state version, which is far dearer than one at the
	// tip, and nothing public reads history through this route.
	queryMaxHeightAge = 100
	// queryMaxConcurrent is how many module queries this proxy has in flight at once. The rest
	// are answered 503 with Retry-After, so a burst cannot queue up work on the chain process.
	queryMaxConcurrent = 16
	// queryMaxPageLimit is the most entries a paginated wallet query may ask for in one page. The
	// SDK's own default, for a request that sets none, is 100.
	queryMaxPageLimit = 100
	// heightCacheTTL is how long the latest height read for the window check is reused.
	heightCacheTTL = time.Second
)

// publicQuery lists every Query method the public route serves. Each is a point lookup, a constant
// computation, or a walk the module bounds with a server-side cap. A method is added here on
// purpose: TestQuery_everyEmbeddedMethodIsClassified fails for one that is in neither this list nor
// withheldQuery, so a new module query cannot become public by being embedded.
var publicQuery = names(
	"orama.archive.v1.Query/Params", "orama.archive.v1.Query/Range", "orama.archive.v1.Query/LastArchivedHeight",
	"orama.archive.v1.Query/RetainHeight",
	"orama.cnft.v1.Query/Collection", "orama.cnft.v1.Query/Tree", "orama.cnft.v1.Query/Decompressed",
	"orama.cnft.v1.Query/Snapshots", // capped at MaxSnapshotsPerQuery by x/cnft
	"orama.emission.v1.Query/Params", "orama.emission.v1.Query/CurrentEpoch", "orama.emission.v1.Query/ScheduleAt",
	"orama.emission.v1.Query/CumulativeMinted", "orama.emission.v1.Query/SupplyCapSoFar",
	"orama.fees.v1.Query/Params", "orama.fees.v1.Query/BaseFee", "orama.fees.v1.Query/Earnings",
	"orama.fees.v1.Query/FeeBalance", "orama.fees.v1.Query/Deposit",
	"orama.houses.v1.Query/Params", "orama.houses.v1.Query/Proposal", "orama.houses.v1.Query/Vote",
	"orama.houses.v1.Query/HouseBond", "orama.houses.v1.Query/Enacted",
	"orama.market.v1.Query/Listing", "orama.market.v1.Query/Bid",
	"orama.nodes.v1.Query/Params", "orama.nodes.v1.Query/Operator", "orama.nodes.v1.Query/Node",
	"orama.nodes.v1.Query/Cluster",
	"orama.nodes.v1.Query/NodeUnbondings", // capped at MaxUnbondingsPerQuery by x/nodes
	"orama.power.v1.Query/Params", "orama.power.v1.Query/BootstrapCommittee", "orama.power.v1.Query/Lambda",
	"orama.power.v1.Query/ValidatorPower",
	"orama.relay.v1.Query/Params", "orama.relay.v1.Query/Reporters", "orama.relay.v1.Query/Relay",
	"orama.relay.v1.Query/EpochResult",
	"orama.shielded.v1.Query/Params", "orama.shielded.v1.Query/TreeState", "orama.shielded.v1.Query/NullifierSpent",
	"orama.storage.v1.Query/Params", "orama.storage.v1.Query/Deal", "orama.storage.v1.Query/Slot",
	"orama.storage.v1.Query/Authorization", "orama.storage.v1.Query/Challenges", "orama.storage.v1.Query/EpochMint",
	"orama.storage.v1.Query/Queue", "orama.storage.v1.Query/NodeFailures",
	"orama.token.v1.Query/Params", "orama.token.v1.Query/Token", "orama.token.v1.Query/Frozen",
)

// withheldQuery lists the Query methods the public route never serves: every module's Invariants
// (each walks the module's whole state), and the queries that scan an unbounded set without
// pagination.
var withheldQuery = names(
	"orama.archive.v1.Query/Invariants", "orama.emission.v1.Query/Invariants", "orama.fees.v1.Query/Invariants",
	"orama.houses.v1.Query/Invariants", "orama.market.v1.Query/Invariants", "orama.nodes.v1.Query/Invariants",
	"orama.power.v1.Query/Invariants", "orama.relay.v1.Query/Invariants", "orama.shielded.v1.Query/Invariants",
	"orama.storage.v1.Query/Invariants", "orama.token.v1.Query/Invariants",
	// Tiers reads every operator and each one's service days.
	"orama.houses.v1.Query/Tiers",
	// Pools walks every pool.
	"orama.shielded.v1.Query/Pools",
)

func names(list ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(list))
	for _, n := range list {
		out[n] = struct{}{}
	}
	return out
}

var (
	queryOnce    sync.Once
	queryMethods map[string]struct{}
	queryErr     error
)

// queryAllowed is the set of "<service>/<method>" names the public route serves: the embedded
// Orama Query methods that publicQuery lists, and the embedded cosmos-sdk and wasmd ones that
// walletQuery lists.
func queryAllowed() (map[string]struct{}, error) {
	queryOnce.Do(func() {
		orama, err := chainread.Methods()
		if err != nil {
			queryErr = err
			return
		}
		sdk, err := chainread.SDKMethods()
		if err != nil {
			queryErr = err
			return
		}
		queryMethods = make(map[string]struct{}, len(orama)+len(walletQuery))
		for _, n := range orama {
			if _, ok := publicQuery[n]; ok {
				queryMethods[n] = struct{}{}
			}
		}
		for _, n := range sdk {
			if _, ok := walletQuery[n]; ok {
				queryMethods[n] = struct{}{}
			}
		}
	})
	return queryMethods, queryErr
}

func (p *Proxy) serveQuery(w http.ResponseWriter, r *http.Request, name string) {
	allowed, err := queryAllowed()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "chain queries are unavailable")
		return
	}
	if _, ok := allowed[name]; !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.Contains(r.URL.RawQuery, ";") {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	m, err := chainread.Lookup(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	up, ok := queryUpstream(r, m)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	select {
	case p.querySlots <- struct{}{}:
		defer func() { <-p.querySlots }()
	default:
		w.Header().Set("Retry-After", "2")
		writeErr(w, http.StatusServiceUnavailable, "too many chain queries in flight")
		return
	}
	if h := up.Get("height"); h != "" && !p.heightInWindow(w, r, h) {
		return
	}
	_, body, ok := p.fetch(w, r, p.rpc, upstreamABCIQuery, up, queryMaxResponse)
	if !ok {
		return
	}
	out, err := m.DecodeRPC(body)
	if err != nil {
		writeQueryFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// queryUpstream validates the caller's query and builds the abci_query one. It reports false for
// an unknown key, a repeated key, both request forms, an oversized or malformed request, or a
// height that is not a positive integer or 0.
func queryUpstream(r *http.Request, m *chainread.Method) (url.Values, bool) {
	q, ok := singleQuery(r, "data", "json", "height")
	if !ok || (q.Get("data") != "" && q.Get("json") != "") {
		return nil, false
	}
	req, ok := queryRequestBytes(q, m)
	if !ok {
		return nil, false
	}
	if limit, err := m.PageLimit(req); err != nil || limit > queryMaxPageLimit {
		return nil, false
	}
	up := url.Values{}
	up.Set("path", strconv.Quote(m.Path))
	up.Set("prove", "false")
	if len(req) > 0 {
		up.Set("data", "0x"+hex.EncodeToString(req))
	}
	if h := q.Get("height"); h != "" && h != "0" {
		if _, good := parsePositive(h); !good {
			return nil, false
		}
		up.Set("height", h)
	}
	return up, true
}

func queryRequestBytes(q url.Values, m *chainread.Method) ([]byte, bool) {
	if raw := q.Get("json"); raw != "" {
		if len(raw) > queryMaxRequest {
			return nil, false
		}
		req, err := m.EncodeRequest(raw)
		return req, err == nil
	}
	raw := strings.TrimRight(q.Get("data"), "=")
	if raw == "" {
		return nil, true
	}
	if len(raw) > base64.RawURLEncoding.EncodedLen(queryMaxRequest) {
		return nil, false
	}
	req, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		if req, err = base64.RawURLEncoding.DecodeString(raw); err != nil {
			return nil, false
		}
	}
	return req, m.CheckRequest(req) == nil
}

// writeQueryFailure answers a query the node refused. A key that is not on chain is a 404. The
// node's own message is not repeated: it can carry paths and store details.
func writeQueryFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, chainread.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found on chain")
		return
	}
	if errors.Is(err, chainread.ErrInvalidRequest) {
		writeErr(w, http.StatusBadRequest, "the chain refused the request")
		return
	}
	writeErr(w, http.StatusBadGateway, "chain query failed")
}

// heightInWindow checks that the height a query asks for is within queryMaxHeightAge of the latest
// block. It writes the refusal itself and reports false when it is not, or when the latest height
// cannot be read.
func (p *Proxy) heightInWindow(w http.ResponseWriter, r *http.Request, raw string) bool {
	height, _ := parsePositive(raw)
	latest, ok := p.latestHeight(w, r)
	if !ok {
		return false
	}
	if height < latest-queryMaxHeightAge {
		writeErr(w, http.StatusBadRequest, "height is older than the last "+strconv.Itoa(queryMaxHeightAge)+" blocks")
		return false
	}
	return true
}

// latestHeight reads the node's latest block height from /status and reuses it for
// heightCacheTTL, so a stream of height queries costs one status call a second.
func (p *Proxy) latestHeight(w http.ResponseWriter, r *http.Request) (int64, bool) {
	p.heightMu.Lock()
	if time.Since(p.heightAt) < heightCacheTTL && p.heightVal > 0 {
		h := p.heightVal
		p.heightMu.Unlock()
		return h, true
	}
	p.heightMu.Unlock()
	_, body, ok := p.fetch(w, r, p.rpc, upstreamStatus, nil, statusMaxBody)
	if !ok {
		return 0, false
	}
	var status struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		writeErr(w, http.StatusBadGateway, "chain request failed")
		return 0, false
	}
	h, good := parsePositive(status.Result.SyncInfo.LatestBlockHeight)
	if !good {
		writeErr(w, http.StatusBadGateway, "chain request failed")
		return 0, false
	}
	p.heightMu.Lock()
	p.heightVal, p.heightAt = h, time.Now()
	p.heightMu.Unlock()
	return h, true
}
