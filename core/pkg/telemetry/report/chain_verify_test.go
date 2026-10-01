package report

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChainNodeID_matchesCometBFT(t *testing.T) {
	got, err := chainNodeID(writeFixtureNodeKey(t, fixtureNodeKey))
	if err != nil {
		t.Fatal(err)
	}
	if got != fixtureNodeID {
		t.Errorf("node id %s, want %s (CometBFT's p2p.NodeKey.ID)", got, fixtureNodeID)
	}
}

func TestChainNodeID_malformedKeys(t *testing.T) {
	keyOf := func(typ string, raw []byte) string {
		return fmt.Sprintf(`{"priv_key":{"type":%q,"value":%q}}`, typ, base64.StdEncoding.EncodeToString(raw))
	}
	seedOnly := make([]byte, 64)
	seedOnly[0] = 1 // a seed but an all-zero public key half
	for name, tc := range map[string]struct {
		content string
		want    string
	}{
		"not json":      {`{"priv_key":`, "decode the chain node key"},
		"empty file":    {``, "decode the chain node key"},
		"secp256k1 key": {keyOf("tendermint/PrivKeySecp256k1", make([]byte, 32)), "is not a tendermint/PrivKeyEd25519"},
		"short key":     {keyOf(chainNodeKeyType, make([]byte, 32)), "is 32 bytes, want 64"},
		"no public key": {keyOf(chainNodeKeyType, seedOnly), "carries no public key"},
	} {
		_, err := chainNodeID(writeFixtureNodeKey(t, tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestChainNodeID_missingFile(t *testing.T) {
	_, err := chainNodeID(filepath.Join(t.TempDir(), "node_key.json"))
	if err == nil || !strings.Contains(err.Error(), "read the chain node key") {
		t.Errorf("err = %v", err)
	}
}

// Another process holding the RPC port answers as a different node: none of
// what it says may reach the report.
func TestQueryChainRPC_mismatchedNodeIDIsNotResponsive(t *testing.T) {
	f := &fakeChain{nodeID: "00000000000000000000000000000000000000aa", height: 77,
		latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	r := runFakeChain(t, f)
	if r.Responsive || !strings.Contains(r.Error, "node_info.id is not this node's id") {
		t.Fatalf("responsive=%v error=%q", r.Responsive, r.Error)
	}
	if r.LatestHeight != 0 || r.ChainID != "" || len(r.Validators) != 0 {
		t.Errorf("an impostor's answer reached the report: %+v", r)
	}
}

// statusWith serves /status with the given node_info fields and the normal
// fixtures elsewhere.
func statusWith(t *testing.T, network, version string) *ChainReport {
	t.Helper()
	f := &fakeChain{height: 10, latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	mux := http.NewServeMux()
	mux.Handle("/", f.handler())
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"id":%q,"network":%s,"version":%s},
 "sync_info":{"latest_block_height":"10","latest_block_time":%q,"catching_up":false},
 "validator_info":{"address":"5D6A0C7E9B00000000000000000000000000AA01","voting_power":"10"}}}`,
			fixtureNodeID, jsonString(t, network), jsonString(t, version), f.latest.Format(time.RFC3339Nano))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := &ChainReport{}
	queryChainRPC(context.Background(), srv.URL, fixtureNodeID, r, time.Now())
	return r
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestQueryChainRPC_malformedStatusFields(t *testing.T) {
	const injected = "<script>alert(1)</script>"
	for name, tc := range map[string]struct {
		network, version, want string
	}{
		"markup chain id":   {injected, "0.39.4", "node_info.network"},
		"empty chain id":    {"", "0.39.4", "node_info.network"},
		"long chain id":     {strings.Repeat("a", 65), "0.39.4", "node_info.network"},
		"markup version":    {"orama-stagenet-1", injected, "node_info.version"},
		"empty version":     {"orama-stagenet-1", "", "node_info.version"},
		"version with ansi": {"orama-stagenet-1", "0.39.4\x1b[2J", "node_info.version"},
	} {
		r := statusWith(t, tc.network, tc.version)
		if r.Responsive || !strings.Contains(r.Error, tc.want) {
			t.Errorf("%s: responsive=%v error=%q, want an error naming %s", name, r.Responsive, r.Error, tc.want)
		}
		if strings.Contains(r.Error, "<script>") || r.ChainID != "" || r.NodeVersion != "" {
			t.Errorf("%s: the malformed value reached the report: %+v", name, r)
		}
	}
}

func TestQueryChainRPC_boundaryStatusFieldsAccepted(t *testing.T) {
	r := statusWith(t, strings.Repeat("a", 64), "0.39.4+build_1-rc.2")
	if !r.Responsive {
		t.Fatalf("a 64-character chain id and a build-suffixed version were refused: %s", r.Error)
	}
}

// validatorsWith serves /validators with one page holding addrs.
func validatorsWith(t *testing.T, addrs []string, total int) *ChainReport {
	t.Helper()
	f := &fakeChain{height: 10, latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	mux := http.NewServeMux()
	mux.Handle("/", f.handler())
	mux.HandleFunc("/validators", func(w http.ResponseWriter, _ *http.Request) {
		entries := make([]string, len(addrs))
		for i, a := range addrs {
			entries[i] = fmt.Sprintf(`{"address":%q,"voting_power":"1"}`, a)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":-1,"result":{"validators":[%s],"total":"%d"}}`,
			strings.Join(entries, ","), total)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := &ChainReport{}
	queryChainRPC(context.Background(), srv.URL, fixtureNodeID, r, time.Now())
	return r
}

func TestQueryChainRPC_malformedValidatorAddress(t *testing.T) {
	for name, addr := range map[string]string{
		"lower-case hex": "5d6a0c7e9b00000000000000000000000000aa01",
		"too short":      "5D6A0C7E9B",
		"markup":         `<img src=x onerror=alert(1)>` + strings.Repeat("A", 12),
		"empty":          "",
	} {
		r := validatorsWith(t, []string{"7E1F00AA2100000000000000000000000000AA02", addr}, 2)
		if r.Responsive || !strings.Contains(r.Error, "validator address") {
			t.Errorf("%s: responsive=%v error=%q", name, r.Responsive, r.Error)
		}
		if strings.Contains(r.Error, "<img") {
			t.Errorf("%s: the address was echoed: %q", name, r.Error)
		}
	}
}

// One page claiming more validators than the report keeps is refused rather
// than stored.
func TestQueryChainRPC_validatorCountCapped(t *testing.T) {
	addrs := make([]string, chainMaxValidators+1)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("%040X", i)
	}
	r := validatorsWith(t, addrs, len(addrs))
	if r.Responsive || !strings.Contains(r.Error, fmt.Sprintf("over %d members", chainMaxValidators)) {
		t.Fatalf("responsive=%v error=%q", r.Responsive, r.Error)
	}
	if len(r.Validators) != 0 {
		t.Errorf("kept %d validators past the cap", len(r.Validators))
	}
}

func TestShortChainError_replacesUnprintableRunes(t *testing.T) {
	got := shortChainError(fmt.Errorf("Internal error\x1b]0;owned\x07 \u202edaolnwod\u200b"))
	if strings.ContainsAny(got, "\x1b\x07\u202e\u200b") {
		t.Errorf("unprintable runes survived: %q", got)
	}
	if !strings.HasPrefix(got, "Internal error?]0;owned? ?daolnwod?") {
		t.Errorf("printable text changed: %q", got)
	}
}

// writeFixtureNodeKey writes content as a node_key.json and returns its path.
func writeFixtureNodeKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node_key.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectChain_missingNodeKeyIsAnError(t *testing.T) {
	f := &fakeChain{height: 100, latest: time.Now().UTC(), interval: time.Second, votingPower: 10}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	stubChainNode(t, srv.URL, "loaded", nil, "active")
	chainNodeKeyPath = filepath.Join(t.TempDir(), "absent", "node_key.json")
	r := collectChain()
	if r == nil || r.Responsive || !strings.Contains(r.Error, "chain node key") {
		t.Fatalf("got %+v; want an unresponsive report naming the node key", r)
	}
	if r.LatestHeight != 0 {
		t.Errorf("the RPC was read without a node id to check it against: height %d", r.LatestHeight)
	}
}
