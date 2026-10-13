package chainread

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// TreeFrontier is a public module query: a wallet downloads the right edge of the note-commitment
// tree through the same route as the other reads, and the bytes fields arrive as base64.
func TestQuery_treeFrontierIsPublicAndDecodesItsBytes(t *testing.T) {
	root := bytes.Repeat([]byte{0xab}, 32)
	frontier := append([]byte{1, 0, 0, 0, 0, 0, 0, 0}, bytes.Repeat([]byte{0xcd}, 64)...)
	// QueryTreeFrontierResponse{height: 9, tree_size: 2, current_root, frontier}
	answer := []byte{0x08, 9, 0x10, 2, 0x1a, byte(len(root))}
	answer = append(answer, root...)
	answer = append(answer, 0x22, byte(len(frontier)))
	answer = append(answer, frontier...)

	up := &abciUpstream{value: answer}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, "/v1/chain/query/orama.shielded.v1.Query/TreeFrontier")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Height      string `json:"height"`
		TreeSize    string `json:"tree_size"`
		CurrentRoot string `json:"current_root"`
		Frontier    string `json:"frontier"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Height != "9" || got.TreeSize != "2" {
		t.Fatalf("height %q size %q", got.Height, got.TreeSize)
	}
	if b, err := base64.StdEncoding.DecodeString(got.CurrentRoot); err != nil || !bytes.Equal(b, root) {
		t.Fatalf("current_root %q: %v", got.CurrentRoot, err)
	}
	if b, err := base64.StdEncoding.DecodeString(got.Frontier); err != nil || !bytes.Equal(b, frontier) {
		t.Fatalf("frontier %q: %v", got.Frontier, err)
	}
	if q := up.calls[0]; q.Get("path") != `"/orama.shielded.v1.Query/TreeFrontier"` || q.Get("prove") != "false" {
		t.Fatalf("upstream query %v", q)
	}
}

// SubtreeRoots is a public module query: a wallet pages the roots of the filled subtrees with the
// JSON form of the request (the 64-bit start_index as a string, as proto JSON writes it), and the
// 32-byte roots arrive as base64 beside their index and filling height.
func TestQuery_subtreeRootsIsPublicAndTakesItsPaging(t *testing.T) {
	root := bytes.Repeat([]byte{0xee}, 32)
	// QuerySubtreeRootsResponse{roots: [SubtreeRoot{index: 1, root, height: 5}], total: 2}
	entry := append([]byte{0x08, 1, 0x12, byte(len(root))}, root...)
	entry = append(entry, 0x18, 5)
	answer := append([]byte{0x0a, byte(len(entry))}, entry...)
	answer = append(answer, 0x10, 2)

	up := &abciUpstream{value: answer}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, "/v1/chain/query/orama.shielded.v1.Query/SubtreeRoots?json="+url.QueryEscape(`{"start_index":"1","limit":10}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Roots []struct {
			Index  string `json:"index"`
			Root   string `json:"root"`
			Height string `json:"height"`
		} `json:"roots"`
		Total string `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Roots) != 1 || got.Total != "2" || got.Roots[0].Index != "1" || got.Roots[0].Height != "5" {
		t.Fatalf("answer %+v", got)
	}
	if b, err := base64.StdEncoding.DecodeString(got.Roots[0].Root); err != nil || !bytes.Equal(b, root) {
		t.Fatalf("root %q: %v", got.Roots[0].Root, err)
	}
	// The request the node received is QuerySubtreeRootsRequest{start_index: 1, limit: 10}. Protobuf
	// does not fix the order fields are written in, so the fields are compared, not the bytes.
	q := up.calls[0]
	if q.Get("path") != `"/orama.shielded.v1.Query/SubtreeRoots"` {
		t.Fatalf("upstream query %v", q)
	}
	if fields := upstreamVarints(t, q.Get("data")); len(fields) != 2 || fields[1] != 1 || fields[2] != 10 {
		t.Fatalf("upstream request fields %v, want start_index 1 and limit 10", fields)
	}

	if rr := getQuery(p, http.MethodGet, "/v1/chain/query/orama.shielded.v1.Query/SubtreeRoots?json="+url.QueryEscape(`{"start_index":-1}`)); rr.Code != http.StatusBadRequest {
		t.Fatalf("a negative start_index answered %d", rr.Code)
	}
}

// upstreamVarints decodes the 0x-prefixed hex of a request the node received into its varint fields
// by number; it fails the test on any other wire type or a malformed message.
func upstreamVarints(t *testing.T, data string) map[protowire.Number]uint64 {
	t.Helper()
	raw, err := hex.DecodeString(strings.TrimPrefix(data, "0x"))
	if err != nil {
		t.Fatalf("upstream data %q: %v", data, err)
	}
	fields := map[protowire.Number]uint64{}
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 || typ != protowire.VarintType {
			t.Fatalf("upstream data %q: not a varint field", data)
		}
		v, m := protowire.ConsumeVarint(raw[n:])
		if m < 0 {
			t.Fatalf("upstream data %q: bad varint", data)
		}
		fields[num] = v
		raw = raw[n+m:]
	}
	return fields
}
