// Package apphash checks that every validator of the localnet computed the same state at every
// height of a run. A load that makes two nodes disagree is a consensus bug, and the report must say
// so loudly instead of printing throughput numbers over it.
package apphash

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// MinNodes is the fewest nodes a comparison needs: with one there is nothing to agree with.
const MinNodes = 2

// Header field names a Mismatch reports.
const (
	FieldAppHash         = "app_hash"
	FieldLastResultsHash = "last_results_hash"
	FieldValidatorsHash  = "validators_hash"
	FieldBlockHash       = "block_hash"
)

// HeaderView is what one node says about the block at one height. The header at height h carries
// the app hash of the state after height h-1, so comparing every height compares every state.
type HeaderView struct {
	Node            string
	Height          int64
	AppHash         []byte
	LastResultsHash []byte
	ValidatorsHash  []byte
	BlockHash       []byte
}

// Mismatch is one field on which the nodes disagreed at one height.
type Mismatch struct {
	Height int64             `json:"height"`
	Field  string            `json:"field"`
	Values map[string]string `json:"values"`
}

// Result is the outcome of comparing a range of heights.
type Result struct {
	From           int64      `json:"from_height"`
	To             int64      `json:"to_height"`
	Nodes          []string   `json:"nodes"`
	HeightsChecked int        `json:"heights_checked"`
	Mismatches     []Mismatch `json:"mismatches"`
}

// Agree reports whether every height was checked and no node disagreed.
func (r Result) Agree() bool { return r.HeightsChecked > 0 && len(r.Mismatches) == 0 }

// Fetcher reads one node's header at a height.
type Fetcher func(ctx context.Context, node string, height int64) (HeaderView, error)

// CompareHeight compares the views of one height. It is an error when there are fewer than MinNodes
// views or a view is for another height; a disagreement is a Mismatch, not an error.
func CompareHeight(height int64, views []HeaderView) ([]Mismatch, error) {
	if len(views) < MinNodes {
		return nil, fmt.Errorf("height %d: %d views, need at least %d to compare", height, len(views), MinNodes)
	}
	for _, v := range views {
		if v.Height != height {
			return nil, fmt.Errorf("node %s answered height %d for a request for %d", v.Node, v.Height, height)
		}
	}
	fields := []struct {
		name string
		get  func(HeaderView) []byte
	}{
		{FieldAppHash, func(v HeaderView) []byte { return v.AppHash }},
		{FieldLastResultsHash, func(v HeaderView) []byte { return v.LastResultsHash }},
		{FieldValidatorsHash, func(v HeaderView) []byte { return v.ValidatorsHash }},
		{FieldBlockHash, func(v HeaderView) []byte { return v.BlockHash }},
	}
	var out []Mismatch
	for _, f := range fields {
		if m, differs := compareField(height, f.name, views, f.get); differs {
			out = append(out, m)
		}
	}
	return out, nil
}

func compareField(height int64, name string, views []HeaderView, get func(HeaderView) []byte) (Mismatch, bool) {
	first := get(views[0])
	same := true
	values := make(map[string]string, len(views))
	for _, v := range views {
		values[v.Node] = hex.EncodeToString(get(v))
		if !bytes.Equal(first, get(v)) {
			same = false
		}
	}
	return Mismatch{Height: height, Field: name, Values: values}, !same
}

// CompareRange fetches every node's header at every height in [from, to] and compares them. A
// fetch that fails stops the comparison with an error: a node that cannot answer has not agreed.
func CompareRange(ctx context.Context, nodes []string, from, to int64, fetch Fetcher) (Result, error) {
	res := Result{From: from, To: to, Nodes: append([]string(nil), nodes...)}
	if len(nodes) < MinNodes {
		return res, fmt.Errorf("%d nodes given, need at least %d to compare app hashes", len(nodes), MinNodes)
	}
	if from < 1 || to < from {
		return res, fmt.Errorf("height range [%d, %d] is empty or starts below 1", from, to)
	}
	for h := from; h <= to; h++ {
		views := make([]HeaderView, 0, len(nodes))
		for _, n := range nodes {
			v, err := fetch(ctx, n, h)
			if err != nil {
				return res, fmt.Errorf("read the header at %d from %s: %w", h, n, err)
			}
			views = append(views, v)
		}
		mism, err := CompareHeight(h, views)
		if err != nil {
			return res, err
		}
		res.Mismatches = append(res.Mismatches, mism...)
		res.HeightsChecked++
	}
	sort.SliceStable(res.Mismatches, func(i, j int) bool { return res.Mismatches[i].Height < res.Mismatches[j].Height })
	return res, nil
}

// ErrDisagree is what Check returns for a range on which the nodes disagreed.
var ErrDisagree = errors.New("validators disagree on state")

// Check is nil when the result is a complete agreement, and otherwise an error naming the first
// height and field that differ.
func (r Result) Check() error {
	if r.HeightsChecked == 0 {
		return errors.New("no height was compared")
	}
	if len(r.Mismatches) == 0 {
		return nil
	}
	m := r.Mismatches[0]
	return fmt.Errorf("%w: %d mismatch(es), first at height %d on %s", ErrDisagree, len(r.Mismatches), m.Height, m.Field)
}
