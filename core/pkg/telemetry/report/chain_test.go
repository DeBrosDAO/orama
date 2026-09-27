package report

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Fixtures are shaped like CometBFT v0.39 RPC answers: a JSON-RPC envelope,
// every integer a string, times RFC 3339.
const statusFixture = `{"jsonrpc":"2.0","id":-1,"result":{
 "node_info":{"protocol_version":{"p2p":"9","block":"11","app":"0"},"id":"%s","listen_addr":"tcp://10.0.0.1:31000",
  "network":"orama-stagenet-1","version":"0.39.4","channels":"40202122233038606100","moniker":"athena",
  "other":{"tx_index":"on","rpc_address":"tcp://127.0.0.1:31001"}},
 "sync_info":{"latest_block_hash":"AB12","latest_app_hash":"CD34","latest_block_height":"%d",
  "latest_block_time":"%s","earliest_block_height":"1","catching_up":false},
 "validator_info":{"address":"5D6A0C7E9B00000000000000000000000000AA01","pub_key":{"type":"tendermint/PubKeyEd25519","value":"q83v"},"voting_power":"%d"}}}`

const netInfoFixture = `{"jsonrpc":"2.0","id":-1,"result":{"listening":true,"listeners":["Listener(@10.0.0.1:31000)"],"n_peers":"2","peers":[]}}`

const mempoolFixture = `{"jsonrpc":"2.0","id":-1,"result":{"n_txs":"3","total":"3","total_bytes":"612","txs":null}}`

const validatorsFixture = `{"jsonrpc":"2.0","id":-1,"result":{"block_height":"%d","validators":[
 {"address":"5D6A0C7E9B00000000000000000000000000AA01","pub_key":{"type":"tendermint/PubKeyEd25519","value":"q83v"},"voting_power":"10","proposer_priority":"-5"},
 {"address":"7E1F00AA2100000000000000000000000000AA02","pub_key":{"type":"tendermint/PubKeyEd25519","value":"r94w"},"voting_power":"10","proposer_priority":"5"},
 {"address":"9A3C11BB4200000000000000000000000000AA03","pub_key":{"type":"tendermint/PubKeyEd25519","value":"s05x"},"voting_power":"5","proposer_priority":"0"}],
 "count":"3","total":"3"}}`

// fixtureNodeKey is a CometBFT v0.39 node_key.json (written by p2p.NodeKey.SaveAs
// for the ed25519 seed 0x01..0x20) and fixtureNodeID the id CometBFT's
// p2p.NodeKey.ID derives from it. Both come from CometBFT itself, so the test
// checks chainNodeID against the real derivation, not a copy of it.
const (
	fixtureNodeKey = `{"priv_key":{"type":"tendermint/PrivKeyEd25519","value":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyB5tVYuj+ZU+UB4sRLoqYunkB+FOuaVvtfg45ELrQSWZA=="}}`
	fixtureNodeID  = "65b60673d6ed884bf01c2c222d82ada0740f29ac"
)

// fakeChain serves a chain at height with blocks every interval ending at
// latest, and records the /blockchain query it was asked. nodeID is the id
// /status answers with; empty means fixtureNodeID.
type fakeChain struct {
	nodeID      string
	height      int64
	latest      time.Time
	interval    time.Duration
	votingPower int64
	blockQuery  string
}

func (f *fakeChain) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		id := f.nodeID
		if id == "" {
			id = fixtureNodeID
		}
		fmt.Fprintf(w, statusFixture, id, f.height, f.latest.Format(time.RFC3339Nano), f.votingPower)
	})
	mux.HandleFunc("/net_info", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, netInfoFixture) })
	mux.HandleFunc("/num_unconfirmed_txs", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mempoolFixture) })
	mux.HandleFunc("/validators", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, validatorsFixture, f.height)
	})
	mux.HandleFunc("/blockchain", func(w http.ResponseWriter, r *http.Request) {
		f.blockQuery = r.URL.RawQuery
		var minH, maxH int64
		fmt.Sscanf(r.URL.Query().Get("minHeight"), "%d", &minH)
		fmt.Sscanf(r.URL.Query().Get("maxHeight"), "%d", &maxH)
		minH = max(minH, maxH-chainBlockWindow+1) // CometBFT's 20-meta limit
		var metas []string
		for h := maxH; h >= minH; h-- { // newest first, as CometBFT answers
			t := f.latest.Add(-time.Duration(f.height-h) * f.interval)
			metas = append(metas, fmt.Sprintf(`{"block_id":{"hash":"AA"},"block_size":"512","header":{"chain_id":"orama-stagenet-1","height":"%d","time":"%s"},"num_txs":"0"}`,
				h, t.Format(time.RFC3339Nano)))
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":-1,"result":{"last_height":"%d","block_metas":[%s]}}`, f.height, strings.Join(metas, ","))
	})
	return mux
}

func runFakeChain(t *testing.T, f *fakeChain) *ChainReport {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	r := &ChainReport{ServiceActive: true}
	queryChainRPC(context.Background(), srv.URL, fixtureNodeID, r, f.latest.Add(3*time.Second))
	return r
}

func TestQueryChainRPC_validatorNode(t *testing.T) {
	f := &fakeChain{height: 1234, latest: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), interval: 2 * time.Second, votingPower: 10}
	r := runFakeChain(t, f)
	if !r.Responsive || r.Error != "" {
		t.Fatalf("responsive=%v error=%q", r.Responsive, r.Error)
	}
	if r.ChainID != "orama-stagenet-1" || r.NodeVersion != "0.39.4" || r.LatestHeight != 1234 || r.CatchingUp {
		t.Errorf("status fields: %+v", r)
	}
	if !r.LatestBlockTime.Equal(f.latest) || r.BlockAgeSec != 3 {
		t.Errorf("latest block time %v, age %v; want %v, 3", r.LatestBlockTime, r.BlockAgeSec, f.latest)
	}
	if r.Peers != 2 || r.MempoolTxs != 3 {
		t.Errorf("peers=%d mempool=%d, want 2 and 3", r.Peers, r.MempoolTxs)
	}
	if !r.IsValidator || r.VotingPower != 10 {
		t.Errorf("validator=%v power=%d, want true and 10", r.IsValidator, r.VotingPower)
	}
	if len(r.Validators) != 3 || r.TotalVotingPower != 25 || r.Validators[2].Address != "9A3C11BB4200000000000000000000000000AA03" {
		t.Errorf("validators=%+v total=%d", r.Validators, r.TotalVotingPower)
	}
	if r.AvgBlockTimeSec != 2 {
		t.Errorf("avg block time %v, want 2", r.AvgBlockTimeSec)
	}
	if f.blockQuery != "minHeight=1214&maxHeight=1234" {
		t.Errorf("/blockchain asked %q", f.blockQuery)
	}
}

func TestQueryChainRPC_nonValidatorNode(t *testing.T) {
	r := runFakeChain(t, &fakeChain{height: 50, latest: time.Now().UTC(), interval: time.Second, votingPower: 0})
	if !r.Responsive || r.IsValidator || r.VotingPower != 0 {
		t.Errorf("a full node reported validator=%v power=%d (responsive=%v %q)", r.IsValidator, r.VotingPower, r.Responsive, r.Error)
	}
	if r.TotalVotingPower != 25 {
		t.Errorf("the set's power is read regardless: got %d", r.TotalVotingPower)
	}
}

// Below the window the query must start at block 1, not a negative height
// CometBFT rejects.
func TestQueryChainRPC_heightBelowWindow(t *testing.T) {
	f := &fakeChain{height: 5, latest: time.Now().UTC(), interval: 4 * time.Second, votingPower: 10}
	r := runFakeChain(t, f)
	if !r.Responsive {
		t.Fatalf("error: %s", r.Error)
	}
	if f.blockQuery != "minHeight=1&maxHeight=5" {
		t.Errorf("/blockchain asked %q", f.blockQuery)
	}
	if r.AvgBlockTimeSec != 4 {
		t.Errorf("avg block time %v, want 4", r.AvgBlockTimeSec)
	}
}

// Before the first block there is no age and no interval, and no /blockchain call.
func TestQueryChainRPC_genesisHeightZero(t *testing.T) {
	f := &fakeChain{height: 0, latest: time.Time{}, votingPower: 10}
	r := runFakeChain(t, f)
	if !r.Responsive || r.BlockAgeSec != 0 || r.AvgBlockTimeSec != 0 || !r.LatestBlockTime.IsZero() {
		t.Errorf("height 0: %+v", r)
	}
	if f.blockQuery != "" {
		t.Errorf("/blockchain was asked %q at height 0", f.blockQuery)
	}
}

func TestQueryChainRPC_rpcDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens there any more
	r := &ChainReport{ServiceActive: false}
	queryChainRPC(context.Background(), base, fixtureNodeID, r, time.Now())
	if r.Responsive || !strings.Contains(r.Error, "/status") {
		t.Errorf("responsive=%v error=%q; want unresponsive naming /status", r.Responsive, r.Error)
	}
}

func TestQueryChainRPC_malformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"result":{"node_info":`)
	}))
	t.Cleanup(srv.Close)
	r := &ChainReport{}
	queryChainRPC(context.Background(), srv.URL, fixtureNodeID, r, time.Now())
	if r.Responsive || !strings.Contains(r.Error, "decode") {
		t.Errorf("responsive=%v error=%q; want a decode error", r.Responsive, r.Error)
	}
}

// A later query failing keeps what /status said and names the query.
func TestQueryChainRPC_rpcErrorEnvelope(t *testing.T) {
	f := &fakeChain{height: 30, latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	mux := http.NewServeMux()
	mux.Handle("/", f.handler())
	mux.HandleFunc("/validators", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"height 31 must be less than or equal to the current blockchain height 30"}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := &ChainReport{}
	queryChainRPC(context.Background(), srv.URL, fixtureNodeID, r, time.Now())
	if r.Responsive || !strings.Contains(r.Error, "/validators") || !strings.Contains(r.Error, "Internal error") {
		t.Errorf("responsive=%v error=%q", r.Responsive, r.Error)
	}
	if r.LatestHeight != 30 {
		t.Errorf("the /status fields were dropped: height %d", r.LatestHeight)
	}
}

func TestAvgBlockInterval_edges(t *testing.T) {
	at := func(h int64, sec int) chainBlockMeta {
		var m chainBlockMeta
		m.Header.Height, m.Header.Time = h, time.Unix(int64(sec), 0)
		return m
	}
	for name, tc := range map[string]struct {
		metas []chainBlockMeta
		want  float64
	}{
		"empty":          {nil, 0},
		"one block":      {[]chainBlockMeta{at(7, 100)}, 0},
		"newest first":   {[]chainBlockMeta{at(12, 130), at(11, 125), at(10, 120)}, 5},
		"pruned gap":     {[]chainBlockMeta{at(10, 100), at(14, 120)}, 5},
		"duplicate only": {[]chainBlockMeta{at(3, 10), at(3, 10)}, 0},
	} {
		if got := avgBlockInterval(tc.metas); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestShortChainError_truncatesLongErrors(t *testing.T) {
	long := fmt.Errorf("%s", strings.Repeat("e", chainErrorMaxLen*2))
	if got := shortChainError(long); len(got) > chainErrorMaxLen+len("…") {
		t.Errorf("error kept %d bytes", len(got))
	}
	if got := shortChainError(fmt.Errorf("short")); got != "short" {
		t.Errorf("short error changed: %q", got)
	}
}

// stubChainNode points the collector at rpcBase with systemd answering
// loadState and activeState for the chain unit.
func stubChainNode(t *testing.T, rpcBase, loadState string, loadErr error, activeState string) {
	t.Helper()
	oldBase, oldSystemctl, oldKey := chainRPCBase, chainSystemctl, chainNodeKeyPath
	t.Cleanup(func() { chainRPCBase, chainSystemctl, chainNodeKeyPath = oldBase, oldSystemctl, oldKey })
	chainRPCBase = rpcBase
	chainNodeKeyPath = writeFixtureNodeKey(t, fixtureNodeKey)
	chainSystemctl = func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "show":
			return loadState, loadErr
		case "is-active":
			if activeState != "active" {
				return "", fmt.Errorf("systemctl: exit status 3")
			}
			return activeState, nil
		}
		return "", fmt.Errorf("unexpected systemctl %v", args)
	}
}

func TestCollectChain_noChainUnitIsNil(t *testing.T) {
	stubChainNode(t, "http://127.0.0.1:1", chainUnitNotFound, nil, "inactive")
	if r := collectChain(); r != nil {
		t.Errorf("a node without the chain unit reported %+v", r)
	}
}

func TestCollectChain_systemctlFailureIsReported(t *testing.T) {
	stubChainNode(t, "http://127.0.0.1:1", "", fmt.Errorf("systemctl: executable file not found"), "")
	r := collectChain()
	if r == nil || r.Responsive || !strings.Contains(r.Error, "load state") {
		t.Errorf("got %+v; want a report naming the failed load-state read", r)
	}
}

func TestCollectChain_activeUnit(t *testing.T) {
	f := &fakeChain{height: 100, latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	stubChainNode(t, srv.URL, "loaded", nil, "active")
	r := collectChain()
	if r == nil || !r.ServiceActive || !r.Responsive || r.LatestHeight != 100 {
		t.Errorf("got %+v", r)
	}
}

func TestCollectChain_stoppedUnit(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	stubChainNode(t, base, "loaded", nil, "inactive")
	r := collectChain()
	if r == nil || r.ServiceActive || r.Responsive || r.Error == "" {
		t.Errorf("got %+v; want an inactive, unresponsive report with an error", r)
	}
}
