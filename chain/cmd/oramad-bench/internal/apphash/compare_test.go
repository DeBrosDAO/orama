package apphash

import (
	"context"
	"errors"
	"testing"
)

func view(node string, h int64, app string) HeaderView {
	return HeaderView{Node: node, Height: h, AppHash: []byte(app), LastResultsHash: []byte("lr"), ValidatorsHash: []byte("vs"), BlockHash: []byte("b" + app)}
}

func TestCompareHeight_agreement(t *testing.T) {
	mism, err := CompareHeight(7, []HeaderView{view("n0", 7, "A"), view("n1", 7, "A"), view("n2", 7, "A")})
	if err != nil || len(mism) != 0 {
		t.Fatalf("got %v, %v", mism, err)
	}
}

func TestCompareHeight_oneNodeDisagrees(t *testing.T) {
	mism, err := CompareHeight(7, []HeaderView{view("n0", 7, "A"), view("n1", 7, "A"), view("n2", 7, "B")})
	if err != nil {
		t.Fatal(err)
	}
	// The app hash and the block hash (which commits to it) differ; the other fields agree.
	fields := map[string]bool{}
	for _, m := range mism {
		fields[m.Field] = true
		if m.Height != 7 || m.Values["n2"] == m.Values["n0"] {
			t.Fatalf("mismatch %+v does not show the disagreement", m)
		}
	}
	if len(mism) != 2 || !fields[FieldAppHash] || !fields[FieldBlockHash] {
		t.Fatalf("mismatches %+v", mism)
	}
}

func TestCompareHeight_errors(t *testing.T) {
	if _, err := CompareHeight(1, []HeaderView{view("n0", 1, "A")}); err == nil {
		t.Fatal("a single view was compared")
	}
	if _, err := CompareHeight(1, nil); err == nil {
		t.Fatal("no views were compared")
	}
	if _, err := CompareHeight(1, []HeaderView{view("n0", 1, "A"), view("n1", 2, "A")}); err == nil {
		t.Fatal("a view of another height was accepted")
	}
}

func TestCompareHeight_emptyHashesAgreeAndNilEqualsEmpty(t *testing.T) {
	a := HeaderView{Node: "a", Height: 1}
	b := HeaderView{Node: "b", Height: 1, AppHash: []byte{}}
	mism, err := CompareHeight(1, []HeaderView{a, b})
	if err != nil || len(mism) != 0 {
		t.Fatalf("got %v, %v", mism, err)
	}
	c := HeaderView{Node: "c", Height: 1, AppHash: []byte{1}}
	mism, _ = CompareHeight(1, []HeaderView{a, c})
	if len(mism) == 0 {
		t.Fatal("an empty hash agreed with a non-empty one")
	}
}

func fetcher(hashes map[string]map[int64]string) Fetcher {
	return func(_ context.Context, node string, h int64) (HeaderView, error) {
		app, ok := hashes[node][h]
		if !ok {
			return HeaderView{}, errors.New("height not served")
		}
		return view(node, h, app), nil
	}
}

func TestCompareRange_checksEveryHeightAndReportsTheFork(t *testing.T) {
	hashes := map[string]map[int64]string{
		"n0": {1: "A", 2: "B", 3: "C"},
		"n1": {1: "A", 2: "B", 3: "C"},
		"n2": {1: "A", 2: "B", 3: "X"},
	}
	res, err := CompareRange(context.Background(), []string{"n0", "n1", "n2"}, 1, 3, fetcher(hashes))
	if err != nil {
		t.Fatal(err)
	}
	if res.HeightsChecked != 3 || res.Agree() {
		t.Fatalf("result %+v", res)
	}
	if res.Mismatches[0].Height != 3 {
		t.Fatalf("first mismatch at %d, want 3", res.Mismatches[0].Height)
	}
	if err := res.Check(); !errors.Is(err, ErrDisagree) {
		t.Fatalf("Check = %v", err)
	}
}

func TestCompareRange_happyPath(t *testing.T) {
	hashes := map[string]map[int64]string{"n0": {5: "A", 6: "B"}, "n1": {5: "A", 6: "B"}}
	res, err := CompareRange(context.Background(), []string{"n0", "n1"}, 5, 6, fetcher(hashes))
	if err != nil || !res.Agree() || res.Check() != nil || res.HeightsChecked != 2 {
		t.Fatalf("result %+v, %v", res, err)
	}
}

func TestCompareRange_errors(t *testing.T) {
	ctx := context.Background()
	hashes := map[string]map[int64]string{"n0": {1: "A"}, "n1": {1: "A"}}
	if _, err := CompareRange(ctx, []string{"n0"}, 1, 1, fetcher(hashes)); err == nil {
		t.Fatal("one node accepted")
	}
	if _, err := CompareRange(ctx, []string{"n0", "n1"}, 0, 1, fetcher(hashes)); err == nil {
		t.Fatal("height 0 accepted")
	}
	if _, err := CompareRange(ctx, []string{"n0", "n1"}, 3, 2, fetcher(hashes)); err == nil {
		t.Fatal("empty range accepted")
	}
	if _, err := CompareRange(ctx, []string{"n0", "n1"}, 1, 2, fetcher(hashes)); err == nil {
		t.Fatal("a node that could not serve a height counted as agreeing")
	}
}

func TestResult_checkRefusesAnEmptyComparison(t *testing.T) {
	if err := (Result{}).Check(); err == nil {
		t.Fatal("a result that compared nothing passed")
	}
}
