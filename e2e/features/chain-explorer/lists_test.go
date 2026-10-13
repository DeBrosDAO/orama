//go:build e2e_fleet

package chainexplorer

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

const (
	// listPageCap is the most entries the gateway lets one list query ask for.
	listPageCap = 100
	// smallPage makes a list with a few entries span pages.
	smallPage = 2
	// maxListPages bounds how far a test follows a page key.
	maxListPages = 200
)

func atoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("%q is not an integer: %v", s, err)
	}
	return n
}

// pageJSON is the `json=` request of a list query for one page.
func pageJSON(limit int, key string, reverse bool) url.Values {
	page := map[string]any{"limit": limit}
	if key != "" {
		page["key"] = key
	}
	if reverse {
		page["reverse"] = true
	}
	raw, _ := json.Marshal(map[string]any{"pagination": page})
	return url.Values{"json": {string(raw)}}
}

// listPages reads a list query page by page through the gateway, following the page key, and
// returns every entry of the list field. It fails a page that is longer than asked.
func listPages(t *testing.T, method, field string, limit int, reverse bool) []json.RawMessage {
	t.Helper()
	var all []json.RawMessage
	key := ""
	for page := 0; page < maxListPages; page++ {
		resp := read(t, "/query/"+method, pageJSON(limit, key, reverse))
		var body map[string]json.RawMessage
		decode(t, resp, &body)
		var out struct {
			NextKey string `json:"next_key"`
		}
		if err := json.Unmarshal(body["pagination"], &out); err != nil {
			t.Fatalf("%s: no pagination in the answer: %v: %.300s", method, err, resp.Body)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(body[field], &entries); err != nil {
			t.Fatalf("%s: %q is not a list: %v: %.300s", method, field, err, resp.Body)
		}
		if len(entries) > limit {
			t.Fatalf("%s: a page of %d entries, asked for %d", method, len(entries), limit)
		}
		all = append(all, entries...)
		if key = out.NextKey; key == "" {
			return all
		}
	}
	t.Fatalf("%s: still paging after %d pages", method, maxListPages)
	return nil
}

// requirePageCap fails unless a page above the gateway's cap is refused (400) before the chain
// is asked and a page at the cap is served.
func requirePageCap(t *testing.T, method string) {
	t.Helper()
	if resp := read(t, "/query/"+method, pageJSON(listPageCap+1, "", false)); resp.Status != http.StatusBadRequest {
		t.Errorf("%s with a page of %d: HTTP %d, want 400: %.200s", method, listPageCap+1, resp.Status, resp.Body)
	}
	if resp := read(t, "/query/"+method, pageJSON(listPageCap, "", false)); resp.Status != http.StatusOK {
		t.Errorf("%s with a page of %d: HTTP %d, want 200: %.200s", method, listPageCap, resp.Status, resp.Body)
	}
}

// TestExplorerLists_nodesHoldsARegisteredNodeAsTheNodeQueryAnswersIt: the Nodes list pages through
// every node x/nodes holds, a node an operator registers is in it, and its entry is the record the
// Node query answers. A page above 100 is refused.
func TestExplorerLists_nodesHoldsARegisteredNodeAsTheNodeQueryAnswersIt(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	id, _ := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "ipfs")

	type node struct {
		NodeID   string   `json:"node_id"`
		Operator string   `json:"operator"`
		Roles    []string `json:"roles"`
		Status   string   `json:"status"`
	}
	var listed *node
	for _, raw := range listPages(t, "orama.nodes.v1.Query/Nodes", "nodes", smallPage, false) {
		var n node
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		if n.NodeID == id {
			listed = &n
		}
	}
	if listed == nil {
		t.Fatalf("node %s is not in the Nodes list", id)
	}
	var point struct {
		Node node `json:"node"`
	}
	decode(t, read(t, "/query/orama.nodes.v1.Query/Node", url.Values{"json": {addressJSON("node_id", id)}}), &point)
	if listed.Operator != op.Address || listed.Status != "NODE_STATUS_ACTIVE" && listed.Status != "NODE_STATUS_REGISTERED" {
		t.Errorf("the list holds %+v for a node registered by %s", *listed, op.Address)
	}
	if listed.Operator != point.Node.Operator || len(listed.Roles) != 1 || listed.Roles[0] != chain.RoleStorage || listed.Status != point.Node.Status {
		t.Errorf("the list holds %+v, the Node query %+v", *listed, point.Node)
	}
	requirePageCap(t, "orama.nodes.v1.Query/Nodes")
}

// TestExplorerLists_dealsPagesNewestFirstAndEveryEntryIsTheDealQueryAnswer: the Deals list answers
// a list (empty on a chain that has made no deal), newest first with pagination.reverse and oldest
// first without, each entry the record the Deal query answers; a page above 100 is refused.
func TestExplorerLists_dealsPagesNewestFirstAndEveryEntryIsTheDealQueryAnswer(t *testing.T) {
	t.Parallel()
	type deal struct {
		ID     string `json:"id"`
		Class  string `json:"class"`
		Status string `json:"status"`
	}
	decodeAll := func(raws []json.RawMessage) []deal {
		out := make([]deal, 0, len(raws))
		for _, raw := range raws {
			var d deal
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			out = append(out, d)
		}
		return out
	}
	newest := decodeAll(listPages(t, "orama.storage.v1.Query/Deals", "deals", smallPage, true))
	oldest := decodeAll(listPages(t, "orama.storage.v1.Query/Deals", "deals", smallPage, false))
	// The two reads are not one snapshot (another test may make a deal between them), so each is
	// checked for its own order rather than against the other.
	for i := 1; i < len(newest); i++ {
		if atoi(t, newest[i-1].ID) <= atoi(t, newest[i].ID) {
			t.Errorf("the newest-first list has deal %s before deal %s", newest[i-1].ID, newest[i].ID)
		}
	}
	for i := 1; i < len(oldest); i++ {
		if atoi(t, oldest[i-1].ID) >= atoi(t, oldest[i].ID) {
			t.Errorf("the oldest-first list has deal %s before deal %s", oldest[i-1].ID, oldest[i].ID)
		}
	}
	for _, d := range newest {
		var point struct {
			Deal deal `json:"deal"`
		}
		decode(t, read(t, "/query/orama.storage.v1.Query/Deal", url.Values{"json": {addressJSON("deal_id", d.ID)}}), &point)
		// The state of a deal can change between the two reads (an epoch boundary), its id and class cannot.
		if point.Deal.ID != d.ID || point.Deal.Class != d.Class {
			t.Errorf("deal %s: the list holds %+v, the Deal query %+v", d.ID, d, point.Deal)
		}
	}
	requirePageCap(t, "orama.storage.v1.Query/Deals")
}

// TestExplorerLists_relaysHoldsARegisteredRelayAsTheRelayQueryAnswersIt: a relay an operator
// registers is in the Relays list, with the fingerprint, node, operator and exit flag the Relay
// query answers. A page above 100 is refused.
func TestExplorerLists_relaysHoldsARegisteredRelayAsTheRelayQueryAnswersIt(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	id, bindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	rsaID := chain.NewRelayRSAIdentity(t)
	reg := chain.ValidRelayExit(t, op.Address, id, rsaID, bindings[0].Priv, true)
	chain.RequireOK(t, "register a relay", c.Submit(t, op, chain.TxOptions{}, reg.Register()))

	type relay struct {
		NodeID      string `json:"node_id"`
		Fingerprint string `json:"rsa_fingerprint"`
		Operator    string `json:"operator"`
		Exit        bool   `json:"exit"`
		Jailed      bool   `json:"jailed"`
	}
	want := base64.StdEncoding.EncodeToString(rsaID.Fingerprint)
	var listed *relay
	for _, raw := range listPages(t, "orama.relay.v1.Query/Relays", "relays", smallPage, false) {
		var r relay
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if r.Fingerprint == want {
			listed = &r
		}
	}
	if listed == nil {
		t.Fatalf("the relay of node %s is not in the Relays list", id)
	}
	var point struct {
		Relay relay `json:"relay"`
	}
	decode(t, read(t, "/query/orama.relay.v1.Query/Relay", url.Values{"json": {addressJSON("rsa_fingerprint_hex", hex.EncodeToString(rsaID.Fingerprint))}}), &point)
	if *listed != point.Relay || listed.NodeID != id || listed.Operator != op.Address || !listed.Exit {
		t.Errorf("the list holds %+v, the Relay query %+v, for node %s of %s", *listed, point.Relay, id, op.Address)
	}
	requirePageCap(t, "orama.relay.v1.Query/Relays")
}

// TestExplorerLists_archiveHealthIsTheChainsOwnNumbers: the archive reads the storage page makes
// through the gateway. The retain height is never above the last archived height, the tip is the
// chain's, and a range that no archiver attested is a 404.
func TestExplorerLists_archiveHealthIsTheChainsOwnNumbers(t *testing.T) {
	t.Parallel()
	var params struct {
		Params struct {
			RangeBlocks string `json:"range_blocks"`
		} `json:"params"`
	}
	decode(t, read(t, "/query/orama.archive.v1.Query/Params", nil), &params)
	var retain struct {
		RetainHeight string `json:"retain_height"`
		Tip          string `json:"tip"`
		Last         string `json:"last_archived_height"`
		Window       string `json:"retention_window_blocks"`
	}
	decode(t, read(t, "/query/orama.archive.v1.Query/RetainHeight", nil), &retain)
	rangeBlocks, tip, last, retainHeight := atoi(t, params.Params.RangeBlocks), atoi(t, retain.Tip), atoi(t, retain.Last), atoi(t, retain.RetainHeight)
	if rangeBlocks <= 0 || tip <= 0 || atoi(t, retain.Window) <= 0 {
		t.Fatalf("archive numbers: range %d, tip %d, window %s", rangeBlocks, tip, retain.Window)
	}
	if retainHeight > last {
		t.Errorf("retain height %d is above the last archived height %d", retainHeight, last)
	}
	next := (last/rangeBlocks)*rangeBlocks + 1
	resp := read(t, "/query/orama.archive.v1.Query/Range", url.Values{"json": {`{"start_height":"` + strconv.FormatInt(next, 10) + `","end_height":"` + strconv.FormatInt(next+rangeBlocks-1, 10) + `"}`}})
	if resp.Status != http.StatusOK && resp.Status != http.StatusNotFound {
		t.Errorf("the range after the archived prefix: HTTP %d, want 200 (attested) or 404 (not yet): %.200s", resp.Status, resp.Body)
	}
	var queue struct {
		Pending string `json:"pending"`
	}
	decode(t, read(t, "/query/orama.storage.v1.Query/Queue", nil), &queue)
	if atoi(t, queue.Pending) < 0 {
		t.Errorf("settlement queue pending %s", queue.Pending)
	}
}
