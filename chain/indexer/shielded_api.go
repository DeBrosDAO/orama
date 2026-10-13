package indexer

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/cockroachdb/pebble"
)

// The shielded scanning routes (/index/v1/shielded/...), both public reads of data every node
// holds: the actions of the shielded transactions, for a wallet that scans the pool, and the roots
// of the tree's filled subtrees, for one that keeps witnesses without every leaf.
const (
	// ActionsDefaultLimit and ActionsMaxLimit bound the actions one page of shielded/actions holds.
	// A page is whole blocks, so a block with more actions than the limit is a page of its own.
	ActionsDefaultLimit = 200
	ActionsMaxLimit     = 1000
	// SubtreesDefaultLimit and SubtreesMaxLimit bound the roots one page of shielded/subtree-roots holds.
	SubtreesDefaultLimit = 100
	SubtreesMaxLimit     = 1000
)

var (
	// indexRE is a non-negative integer of at most 18 digits, a subtree index.
	indexRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})$`)
)

// ShieldedActionsPage is the answer of shielded/actions.
type ShieldedActionsPage struct {
	// StartHeight is the first block the index holds: shielded activity before it is not here.
	StartHeight int64 `json:"start_height"`
	// IndexedHeight is the last block the index holds. A page that reaches it is the end for now.
	IndexedHeight int64 `json:"indexed_height"`
	FromHeight    int64 `json:"from_height"`
	// NextHeight is the from_height of the page after this one, and HasMore says that block is
	// already indexed (a page that stopped at the limit); without HasMore the reader is caught up
	// and asks again from NextHeight when it wants what the chain adds.
	NextHeight int64           `json:"next_height"`
	HasMore    bool            `json:"has_more"`
	Blocks     []ShieldedBlock `json:"blocks"`
}

// ShieldedActions reads a page of the blocks with shielded actions from height from, at most limit
// actions of whole blocks (the first block is returned whole whatever its size), from one snapshot.
func (s *Store) ShieldedActions(from int64, limit int) (ShieldedActionsPage, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	st, err := s.statusAt(snap)
	if err != nil {
		return ShieldedActionsPage{}, err
	}
	page := ShieldedActionsPage{
		StartHeight: st.StartHeight, IndexedHeight: st.Cursor, FromHeight: from,
		NextHeight: max(from, st.Cursor+1), Blocks: []ShieldedBlock{},
	}
	it, err := snap.NewIter(&pebble.IterOptions{
		LowerBound: shieldedActionKey(from, 0, 0), UpperBound: prefixEnd([]byte(pfxShieldAction)),
	})
	if err != nil {
		return page, fmt.Errorf("failed to open index iterator: %w", err)
	}
	total := 0
	var cur *ShieldedBlock
	finish := func() (stop bool, err error) {
		if cur == nil {
			return false, nil
		}
		size, ok, err := getUint(snap, shieldedBlockKey(cur.Height))
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("index holds actions of block %d but not its tree size", cur.Height)
			}
			return false, err
		}
		cur.TreeSize = size
		if total > 0 && total+len(cur.Actions) > limit {
			page.NextHeight, page.HasMore = cur.Height, true
			return true, nil
		}
		total += len(cur.Actions)
		page.Blocks = append(page.Blocks, *cur)
		return false, nil
	}
	stopped := false
	for ok := it.First(); ok && !stopped; ok = it.Next() {
		height, a, err := decodeShieldedAction(it.Key(), it.Value())
		if err != nil {
			return page, joinClose(err, it)
		}
		if cur != nil && cur.Height != height {
			if stopped, err = finish(); err != nil {
				return page, joinClose(err, it)
			}
			cur = nil
			if stopped {
				break
			}
		}
		if cur == nil {
			cur = &ShieldedBlock{Height: height, Actions: []ShieldedAction{}}
		}
		cur.Actions = append(cur.Actions, a)
	}
	if err := closeIter(it); err != nil {
		return page, err
	}
	if !stopped {
		if _, err := finish(); err != nil {
			return page, err
		}
	}
	return page, nil
}

// SubtreeRootsPage is the answer of shielded/subtree-roots.
type SubtreeRootsPage struct {
	IndexedHeight int64 `json:"indexed_height"`
	// SubtreeLeaves is the number of leaves in a subtree, 2^16.
	SubtreeLeaves uint64 `json:"subtree_leaves"`
	// FirstIndex is the first subtree this index has a root for, absent while it has none. The
	// chain reports a root when a subtree fills, so an index that began after earlier subtrees
	// filled, or on a chain from before the report, has none for them.
	FirstIndex *uint64       `json:"first_index,omitempty"`
	StartIndex uint64        `json:"start_index"`
	NextIndex  uint64        `json:"next_index"`
	HasMore    bool          `json:"has_more"`
	Roots      []SubtreeRoot `json:"roots"`
}

// SubtreeRoots reads up to limit subtree roots from index start, from one snapshot.
func (s *Store) SubtreeRoots(start uint64, limit int) (SubtreeRootsPage, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	st, err := s.statusAt(snap)
	if err != nil {
		return SubtreeRootsPage{}, err
	}
	page := SubtreeRootsPage{IndexedHeight: st.Cursor, SubtreeLeaves: 1 << 16, StartIndex: start, NextIndex: start, Roots: []SubtreeRoot{}}
	if first, ok, err := getUint(snap, []byte(keySubtreesFrom)); err != nil {
		return page, err
	} else if ok {
		page.FirstIndex = &first
	}
	it, err := snap.NewIter(&pebble.IterOptions{LowerBound: subtreeKey(start), UpperBound: prefixEnd([]byte(pfxSubtree))})
	if err != nil {
		return page, fmt.Errorf("failed to open index iterator: %w", err)
	}
	for ok := it.First(); ok; ok = it.Next() {
		if len(page.Roots) == limit {
			page.HasMore = true
			break
		}
		key, row := it.Key(), it.Value()
		if len(row) != subtreeRowLen || len(key) != len(pfxSubtree)+8 {
			return page, joinClose(fmt.Errorf("subtree row %x holds %d bytes, want %d", key, len(row), subtreeRowLen), it)
		}
		index := binary.BigEndian.Uint64(key[len(pfxSubtree):])
		page.Roots = append(page.Roots, SubtreeRoot{Index: index, Root: lowerHex(row[:32]), Height: int64(binary.BigEndian.Uint64(row[32:]))})
		page.NextIndex = index + 1
	}
	return page, closeIter(it)
}

func (a *API) serveShieldedActions(w http.ResponseWriter, r *http.Request) {
	q, ok := strictQuery(w, r, "from_height", "limit")
	if !ok {
		return
	}
	from, limit := int64(1), ActionsDefaultLimit
	if raw := q.Get("from_height"); raw != "" {
		if !positiveRE.MatchString(raw) {
			writeError(w, http.StatusBadRequest, "from_height must be a positive integer")
			return
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from_height must be a positive integer")
			return
		}
		from = n
	}
	if raw := q.Get("limit"); raw != "" {
		n, good := boundedInt(raw, ActionsMaxLimit)
		if !good {
			writeError(w, http.StatusBadRequest, "limit is out of range")
			return
		}
		limit = n
	}
	page, err := a.store.ShieldedActions(from, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	if page.HasMore {
		w.Header().Set("Cache-Control", publicMaxAge(SettledMaxAge))
	}
	writeJSON(w, page)
}

func (a *API) serveSubtreeRoots(w http.ResponseWriter, r *http.Request) {
	q, ok := strictQuery(w, r, "start_index", "limit")
	if !ok {
		return
	}
	var start uint64
	limit := SubtreesDefaultLimit
	if raw := q.Get("start_index"); raw != "" {
		if !indexRE.MatchString(raw) {
			writeError(w, http.StatusBadRequest, "start_index must be a non-negative integer")
			return
		}
		start, _ = strconv.ParseUint(raw, 10, 64)
	}
	if raw := q.Get("limit"); raw != "" {
		n, good := boundedInt(raw, SubtreesMaxLimit)
		if !good {
			writeError(w, http.StatusBadRequest, "limit is out of range")
			return
		}
		limit = n
	}
	page, err := a.store.SubtreeRoots(start, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	if page.HasMore {
		w.Header().Set("Cache-Control", publicMaxAge(SettledMaxAge))
	}
	writeJSON(w, page)
}

// strictQuery parses the query and accepts only the named parameters, each at most once.
func strictQuery(w http.ResponseWriter, r *http.Request, names ...string) (url.Values, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad query")
		return nil, false
	}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	for k, vs := range q {
		if !allowed[k] {
			writeError(w, http.StatusBadRequest, "unknown query parameter")
			return nil, false
		}
		if len(vs) != 1 {
			writeError(w, http.StatusBadRequest, "repeated query parameter "+k)
			return nil, false
		}
	}
	return q, true
}

// joinClose returns err with the failure to close it, if any.
func joinClose(err error, it *pebble.Iterator) error {
	if cerr := closeIter(it); cerr != nil {
		return fmt.Errorf("%w; %v", err, cerr)
	}
	return err
}
