//go:build e2e_fleet

package chainexplorer

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// subtreeLeaves is the leaves in one subtree whose root the chain reports (x/shielded
	// types.SubtreeLeaves).
	subtreeLeaves = 1 << 16
	// scanPageLimit is the most actions a page of shielded/actions holds (indexer.ActionsMaxLimit).
	scanPageLimit = 1000
	// nodeBytes is a note commitment, a nullifier, an ephemeral key, a tree node and a root.
	nodeBytes = 32
	// compactBytes and outBytes are the compact ciphertext and the out ciphertext of an action.
	compactBytes = 52
	outBytes     = 80
	// maxFrontierBytes is the longest encoded frontier: position, leaf and 32 ommers.
	maxFrontierBytes = 8 + nodeBytes + 32*nodeBytes
	// scanPageCap bounds the pages one test follows, so a loop that does not end fails.
	scanPageCap = 200
)

// scanAction and scanBlock are the answer of /v1/chain/index/shielded/actions.
type scanAction struct {
	TxIndex       uint32 `json:"tx_index"`
	ActionIndex   uint32 `json:"action_index"`
	Position      uint64 `json:"position"`
	TxHash        string `json:"tx_hash"`
	Nullifier     string `json:"nullifier"`
	Cmx           string `json:"cmx"`
	EphemeralKey  string `json:"ephemeral_key"`
	EncCompact    string `json:"enc_compact"`
	OutCiphertext string `json:"out_ciphertext"`
}

type scanBlock struct {
	Height   int64        `json:"height"`
	TreeSize uint64       `json:"tree_size"`
	Actions  []scanAction `json:"actions"`
}

type scanPage struct {
	StartHeight   int64       `json:"start_height"`
	IndexedHeight int64       `json:"indexed_height"`
	FromHeight    int64       `json:"from_height"`
	NextHeight    int64       `json:"next_height"`
	HasMore       bool        `json:"has_more"`
	Blocks        []scanBlock `json:"blocks"`
}

func actionCount(blocks []scanBlock) (n int) {
	for _, b := range blocks {
		n += len(b.Actions)
	}
	return n
}

// scanAll follows shielded/actions from the index's start to where it is caught up.
func scanAll(t *testing.T, limit int) (scanPage, []scanBlock) {
	t.Helper()
	var blocks []scanBlock
	var first, last scanPage
	from := int64(1)
	for pages := 0; ; pages++ {
		if pages == scanPageCap {
			t.Fatalf("shielded/actions did not end after %d pages", scanPageCap)
		}
		var page scanPage
		decode(t, read(t, "/index/shielded/actions", url.Values{"from_height": {strconv.FormatInt(from, 10)}, "limit": {strconv.Itoa(limit)}}), &page)
		if pages == 0 {
			first = page
		}
		last = page
		blocks = append(blocks, page.Blocks...)
		if !page.HasMore {
			break
		}
		if page.NextHeight <= from {
			t.Fatalf("next_height %d does not move past from_height %d", page.NextHeight, from)
		}
		from = page.NextHeight
	}
	first.IndexedHeight = last.IndexedHeight
	return first, blocks
}

// treeState and treeFrontier are the answers of TreeState and TreeFrontier (64-bit integers are strings,
// bytes are base64).
type treeState struct {
	TreeSize    string `json:"tree_size"`
	CurrentRoot string `json:"current_root"`
}

type treeFrontier struct {
	Height      string `json:"height"`
	TreeSize    string `json:"tree_size"`
	CurrentRoot string `json:"current_root"`
	Frontier    string `json:"frontier"`
}

// treeAt reads TreeState and TreeFrontier of the same height through the gateway (0 is the latest).
func treeAt(t *testing.T, height int64) (treeState, treeFrontier) {
	t.Helper()
	q := url.Values{}
	if height > 0 {
		q.Set("height", strconv.FormatInt(height, 10))
	}
	var state treeState
	var frontier treeFrontier
	decode(t, read(t, "/query/orama.shielded.v1.Query/TreeState", q), &state)
	decode(t, read(t, "/query/orama.shielded.v1.Query/TreeFrontier", q), &frontier)
	return state, frontier
}

// TestShieldedScan_frontierIsTheTreeStatesTree: TreeFrontier is public and answers the right edge
// of the note-commitment tree with the size and root of TreeState at the same height, in the
// encoding the docs give (empty for an empty tree, otherwise the position, the last leaf and one
// ommer per set bit of the position).
func TestShieldedScan_frontierIsTheTreeStatesTree(t *testing.T) {
	t.Parallel()
	_, latest := treeAt(t, 0)
	h, err := strconv.ParseInt(latest.Height, 10, 64)
	if err != nil || h < 1 {
		t.Fatalf("TreeFrontier height %q: %v", latest.Height, err)
	}
	state, frontier := treeAt(t, h)
	if state.TreeSize != frontier.TreeSize || state.CurrentRoot != frontier.CurrentRoot {
		t.Errorf("at height %d TreeState is size %s root %s and TreeFrontier size %s root %s", h, state.TreeSize, state.CurrentRoot, frontier.TreeSize, frontier.CurrentRoot)
	}
	if root, err := base64.StdEncoding.DecodeString(frontier.CurrentRoot); err != nil || len(root) != nodeBytes {
		t.Errorf("current_root %q is not 32 base64 bytes: %v", frontier.CurrentRoot, err)
	}
	size, err := strconv.ParseUint(frontier.TreeSize, 10, 64)
	if err != nil {
		t.Fatalf("tree_size %q: %v", frontier.TreeSize, err)
	}
	raw, err := base64.StdEncoding.DecodeString(frontier.Frontier)
	if err != nil {
		t.Fatalf("frontier %q is not base64: %v", frontier.Frontier, err)
	}
	if size == 0 {
		if len(raw) != 0 {
			t.Errorf("an empty tree has a %d-byte frontier", len(raw))
		}
		return
	}
	position := size - 1
	want := 8 + nodeBytes + nodeBytes*bits.OnesCount64(position)
	if len(raw) != want || len(raw) > maxFrontierBytes {
		t.Fatalf("a tree of %d leaves has a %d-byte frontier, want %d", size, len(raw), want)
	}
	if got := binary.LittleEndian.Uint64(raw); got != position {
		t.Errorf("the frontier's position is %d, want %d", got, position)
	}
}

// TestShieldedScan_actionsAreTheTreesLeaves: every committed shielded action the index holds is one
// leaf of the tree, in the order the chain appended it. From the index's first block the positions
// run on without a gap, each block's tree_size is the position of its last action plus one, and
// the last block's tree_size is the tree's size at the height the index has reached, so a wallet
// that scans these actions holds exactly the chain's leaves. On a chain that has accepted no
// bundle the list is empty and the tree is empty.
func TestShieldedScan_actionsAreTheTreesLeaves(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	head, blocks := scanAll(t, scanPageLimit)
	if head.StartHeight < 1 || head.IndexedHeight < head.StartHeight {
		t.Fatalf("the index spans %d to %d", head.StartHeight, head.IndexedHeight)
	}
	if head.StartHeight == 1 && len(blocks) > 0 && blocks[0].Actions[0].Position != 0 {
		t.Errorf("an index from block 1 lists its first leaf at position %d, not 0", blocks[0].Actions[0].Position)
	}
	var next uint64
	for i, b := range blocks {
		if len(b.Actions) == 0 {
			t.Fatalf("block %d is listed with no action", b.Height)
		}
		if i > 0 && b.Height <= blocks[i-1].Height {
			t.Errorf("block %d follows block %d: blocks are not in order", b.Height, blocks[i-1].Height)
		}
		for j, a := range b.Actions {
			if (i > 0 || j > 0) && a.Position != next {
				t.Errorf("block %d action %d is at position %d, want %d", b.Height, j, a.Position, next)
			}
			next = a.Position + 1
			for name, v := range map[string]struct {
				text string
				n    int
			}{"tx_hash": {a.TxHash, nodeBytes}, "nullifier": {a.Nullifier, nodeBytes}, "cmx": {a.Cmx, nodeBytes},
				"ephemeral_key": {a.EphemeralKey, nodeBytes}, "enc_compact": {a.EncCompact, compactBytes}, "out_ciphertext": {a.OutCiphertext, outBytes}} {
				if raw, err := hex.DecodeString(v.text); err != nil || len(raw) != v.n {
					t.Errorf("block %d action %d: %s is %q, want %d hex bytes", b.Height, j, name, v.text, v.n)
				}
			}
		}
		if b.TreeSize != next {
			t.Errorf("block %d reports a tree of %d leaves after its last action at position %d", b.Height, b.TreeSize, next-1)
		}
	}

	// The tree at the height the index has reached. The read window of a module query is the last
	// 100 blocks; an index that far behind has nothing to compare with.
	state, frontier := treeAt(t, head.IndexedHeight)
	if state.TreeSize != frontier.TreeSize || state.CurrentRoot != frontier.CurrentRoot {
		t.Errorf("at height %d TreeState (%s, %s) and TreeFrontier (%s, %s) disagree", head.IndexedHeight, state.TreeSize, state.CurrentRoot, frontier.TreeSize, frontier.CurrentRoot)
	}
	size, err := strconv.ParseUint(state.TreeSize, 10, 64)
	if err != nil {
		t.Fatalf("tree_size %q: %v", state.TreeSize, err)
	}
	if head.StartHeight == 1 && size != next {
		t.Errorf("the index lists %d leaves from block 1 and the tree holds %d at height %d", next, size, head.IndexedHeight)
	}
	if head.StartHeight > 1 && size < next {
		t.Errorf("the index lists %d leaves and the tree holds only %d", next, size)
	}
}

// TestShieldedScan_pagesAreWholeBlocksWhatEverTheLimit: following next_height reads every action
// exactly once for a small limit and for the largest, a page of several blocks stays within its
// limit, and the page at the end of the index says it is the end.
func TestShieldedScan_pagesAreWholeBlocksWhatEverTheLimit(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	_, all := scanAll(t, scanPageLimit)
	_, small := scanAll(t, 1)
	if actionCount(all) != actionCount(small) || len(all) != len(small) {
		t.Errorf("limit 1000 reads %d actions in %d blocks and limit 1 reads %d in %d", actionCount(all), len(all), actionCount(small), len(small))
	}
	var page scanPage
	decode(t, read(t, "/index/shielded/actions", url.Values{"from_height": {"1"}, "limit": {"1"}}), &page)
	if n := actionCount(page.Blocks); len(page.Blocks) > 1 && n > 1 {
		t.Errorf("a page of several blocks holds %d actions over its limit of 1", n)
	}
	// A from_height past the index is an empty page that does not move backwards.
	ahead := page.IndexedHeight + 1000
	decode(t, read(t, "/index/shielded/actions", url.Values{"from_height": {strconv.FormatInt(ahead, 10)}}), &page)
	if len(page.Blocks) != 0 || page.HasMore || page.NextHeight != ahead {
		t.Errorf("from_height %d past the index answered %d blocks, has_more %v, next_height %d", ahead, len(page.Blocks), page.HasMore, page.NextHeight)
	}
}

// TestShieldedScan_subtreeRootsAreCompleteFromTheFirstIndex: the roots of filled subtrees are listed
// in order with no gap from first_index, each 32 bytes, each filled by a block the index holds; a
// chain whose tree is smaller than one subtree has none, and one with n filled subtrees has a root
// for each from first_index, with the next index equal to the filled count.
func TestShieldedScan_subtreeRootsAreCompleteFromTheFirstIndex(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	var page struct {
		IndexedHeight int64   `json:"indexed_height"`
		SubtreeLeaves uint64  `json:"subtree_leaves"`
		FirstIndex    *uint64 `json:"first_index"`
		StartIndex    uint64  `json:"start_index"`
		NextIndex     uint64  `json:"next_index"`
		HasMore       bool    `json:"has_more"`
		Roots         []struct {
			Index  uint64 `json:"index"`
			Root   string `json:"root"`
			Height int64  `json:"height"`
		} `json:"roots"`
	}
	decode(t, read(t, "/index/shielded/subtree-roots", url.Values{"limit": {"1000"}}), &page)
	if page.SubtreeLeaves != subtreeLeaves {
		t.Errorf("subtree_leaves is %d, want %d", page.SubtreeLeaves, subtreeLeaves)
	}
	if page.Roots == nil || (len(page.Roots) == 0) != (page.FirstIndex == nil) {
		t.Errorf("first_index %v with %d roots", page.FirstIndex, len(page.Roots))
	}
	for i, r := range page.Roots {
		if want := *page.FirstIndex + uint64(i); r.Index != want {
			t.Errorf("root %d has index %d, want %d", i, r.Index, want)
		}
		if raw, err := hex.DecodeString(r.Root); err != nil || len(raw) != nodeBytes {
			t.Errorf("subtree %d root %q is not 32 hex bytes: %v", r.Index, r.Root, err)
		}
		if r.Height < 1 || r.Height > page.IndexedHeight {
			t.Errorf("subtree %d was filled at height %d, outside the index (up to %d)", r.Index, r.Height, page.IndexedHeight)
		}
	}
	// The filled count is the tree's size over the subtree size; the index has a root for each
	// subtree from first_index when it has any.
	_, frontier := treeAt(t, page.IndexedHeight)
	size, err := strconv.ParseUint(frontier.TreeSize, 10, 64)
	if err != nil {
		t.Fatalf("tree_size %q: %v", frontier.TreeSize, err)
	}
	if filled := size / subtreeLeaves; len(page.Roots) > 0 && !page.HasMore && page.NextIndex != filled {
		t.Errorf("the tree has %d filled subtrees and the roots end before index %d", filled, page.NextIndex)
	}
	if size < subtreeLeaves && len(page.Roots) != 0 {
		t.Errorf("a tree of %d leaves has %d subtree roots", size, len(page.Roots))
	}
}

// subtreeRootsAnswer is the answer of the SubtreeRoots query (64-bit integers are strings, the root is
// base64).
type subtreeRootsAnswer struct {
	Roots []struct {
		Index  string `json:"index"`
		Root   string `json:"root"`
		Height string `json:"height"`
	} `json:"roots"`
	Total string `json:"total"`
}

// subtreeRootsAt reads SubtreeRoots from start, at most limit roots, at a height (0 is the latest).
func subtreeRootsAt(t *testing.T, height int64, start uint64, limit int) subtreeRootsAnswer {
	t.Helper()
	q := url.Values{"json": {`{"start_index":"` + strconv.FormatUint(start, 10) + `","limit":` + strconv.Itoa(limit) + `}`}}
	if height > 0 {
		q.Set("height", strconv.FormatInt(height, 10))
	}
	var got subtreeRootsAnswer
	decode(t, read(t, "/query/orama.shielded.v1.Query/SubtreeRoots", q), &got)
	return got
}

// TestShieldedScan_subtreeRootsInStateAreTheChainsAndTheIndexs: the SubtreeRoots query is public and
// answers a root for every filled subtree from index 0, as many as the tree's size over the subtree
// size at the same height, in order, 32 bytes each; a start at the end is an empty page that still
// reports the total; and the roots the index serves are the chain's for every subtree it holds.
func TestShieldedScan_subtreeRootsInStateAreTheChainsAndTheIndexs(t *testing.T) {
	t.Parallel()
	_, latest := treeAt(t, 0)
	h, err := strconv.ParseInt(latest.Height, 10, 64)
	if err != nil || h < 1 {
		t.Fatalf("TreeFrontier height %q: %v", latest.Height, err)
	}
	size, err := strconv.ParseUint(latest.TreeSize, 10, 64)
	if err != nil {
		t.Fatalf("tree_size %q: %v", latest.TreeSize, err)
	}
	filled := size / subtreeLeaves
	got := subtreeRootsAt(t, h, 0, 1000)
	if got.Total != strconv.FormatUint(filled, 10) {
		t.Errorf("at height %d the tree holds %d leaves and SubtreeRoots total is %s, want %d", h, size, got.Total, filled)
	}
	if uint64(len(got.Roots)) != min(filled, 1000) {
		t.Fatalf("SubtreeRoots listed %d roots for %d filled subtrees", len(got.Roots), filled)
	}
	for i, r := range got.Roots {
		if r.Index != strconv.Itoa(i) {
			t.Errorf("root %d has index %s", i, r.Index)
		}
		if raw, err := base64.StdEncoding.DecodeString(r.Root); err != nil || len(raw) != nodeBytes {
			t.Errorf("subtree %s root %q is not 32 base64 bytes: %v", r.Index, r.Root, err)
		}
		if ht, err := strconv.ParseInt(r.Height, 10, 64); err != nil || ht < 1 || ht > h {
			t.Errorf("subtree %s was filled at height %q, outside the chain (up to %d)", r.Index, r.Height, h)
		}
	}
	if past := subtreeRootsAt(t, h, filled, 10); len(past.Roots) != 0 || past.Total != got.Total {
		t.Errorf("a start at the end answered %d roots with total %s, want none and %s", len(past.Roots), past.Total, got.Total)
	}

	requireIndexer(t)
	var page struct {
		FirstIndex *uint64 `json:"first_index"`
		Roots      []struct {
			Index uint64 `json:"index"`
			Root  string `json:"root"`
		} `json:"roots"`
	}
	decode(t, read(t, "/index/shielded/subtree-roots", url.Values{"limit": {"1000"}}), &page)
	for _, r := range page.Roots {
		if r.Index >= uint64(len(got.Roots)) {
			continue // filled after the height read above
		}
		want, err := base64.StdEncoding.DecodeString(got.Roots[r.Index].Root)
		if err != nil || hex.EncodeToString(want) != r.Root {
			t.Errorf("subtree %d: the index serves %s, the chain keeps %s", r.Index, r.Root, got.Roots[r.Index].Root)
		}
	}
}

// TestShieldedScan_refusals: the scanning routes refuse what the indexer and the gateway bound.
func TestShieldedScan_refusals(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	for _, tc := range []struct {
		path  string
		query url.Values
		want  int
	}{
		{"/index/shielded/actions", url.Values{"limit": {"0"}}, http.StatusBadRequest},
		{"/index/shielded/actions", url.Values{"limit": {"1001"}}, http.StatusBadRequest},
		{"/index/shielded/actions", url.Values{"from_height": {"0"}}, http.StatusBadRequest},
		{"/index/shielded/actions", url.Values{"from_height": {"-1"}}, http.StatusBadRequest},
		{"/index/shielded/actions", url.Values{"page": {"1"}}, http.StatusBadRequest},
		{"/index/shielded/actions", url.Values{"from_height": {"1", "2"}}, http.StatusBadRequest},
		{"/index/shielded/subtree-roots", url.Values{"start_index": {"-1"}}, http.StatusBadRequest},
		{"/index/shielded/subtree-roots", url.Values{"limit": {"1001"}}, http.StatusBadRequest},
		{"/index/shielded/subtree-roots", url.Values{"from_height": {"1"}}, http.StatusBadRequest},
		{"/index/shielded", nil, http.StatusNotFound},
		{"/index/shielded/other", nil, http.StatusNotFound},
		{"/query/orama.shielded.v1.Query/Pools", nil, http.StatusNotFound},
		{"/query/orama.shielded.v1.Query/SubtreeRoots", url.Values{"json": {`{"limit":1001}`}}, http.StatusBadRequest},
		{"/query/orama.shielded.v1.Query/SubtreeRoots", url.Values{"json": {`{"start_index":-1}`}}, http.StatusBadRequest},
	} {
		if resp := read(t, tc.path, tc.query); resp.Status != tc.want {
			t.Errorf("GET %s %v: HTTP %d, want %d: %.200s", tc.path, tc.query, resp.Status, tc.want, resp.Body)
		}
	}
	resp := harness.GW(t).MustSend(t, gw.Req{Method: http.MethodPost, Path: chainPrefix + "/index/shielded/actions"})
	if resp.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST /index/shielded/actions: HTTP %d, want 405", resp.Status)
	}
}

// TestShieldedScan_cacheHeaders: a page that has more behind it is fixed and cacheable for ten
// minutes; the page at the head, which the chain keeps adding to, for a block; an error never.
func TestShieldedScan_cacheHeaders(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	for _, path := range []string{"/index/shielded/actions", "/index/shielded/subtree-roots"} {
		resp := read(t, path, nil)
		if resp.Status != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d: %.200s", path, resp.Status, resp.Body)
		}
		switch got := resp.Header.Get("Cache-Control"); got {
		case "public, max-age=2", "public, max-age=600":
		default:
			t.Errorf("GET %s: Cache-Control %q, want public, max-age=2 (the head) or 600 (a page with more behind it)", path, got)
		}
		if bad := read(t, path, url.Values{"limit": {"0"}}); bad.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s?limit=0: Cache-Control %q, want no-store", path, bad.Header.Get("Cache-Control"))
		}
	}
}
