package report

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func stubIndexerStatus(t *testing.T, body string, err error) {
	t.Helper()
	old := globalIndexerGet
	t.Cleanup(func() { globalIndexerGet = old })
	globalIndexerGet = func(_ context.Context, url string) ([]byte, error) {
		if !strings.HasSuffix(url, "/index/v1/operator/status") {
			t.Errorf("the report asked the indexer for %s", url)
		}
		return []byte(body), err
	}
}

func TestCollectGlobal_indexerStatusSaysItIsStuck(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalIndexerUnit: "active"})
	stubIndexerStatus(t, `{"start_height":1,"cursor":40,"earliest":1,"tip":900,"aggregates_from":1,`+
		`"stuck":{"height":41,"reason":"query /x at height 41: node\u0007 pruned","state_pruned":true,"since":"2026-10-11T00:00:00Z","failures":9}}`, nil)
	g := collectGlobal()
	if g == nil || g.Indexer == nil || g.Indexer.Error != "" {
		t.Fatalf("got %+v", g)
	}
	ix := g.Indexer
	if ix.Cursor != 40 || ix.Tip != 900 || ix.Stuck == nil || ix.Stuck.Height != 41 || !ix.Stuck.StatePruned {
		t.Fatalf("indexer %+v stuck %+v", ix, ix.Stuck)
	}
	if strings.ContainsRune(ix.Stuck.Reason, '\a') {
		t.Fatalf("the reason keeps a control character: %q", ix.Stuck.Reason)
	}
}

func TestCollectGlobal_indexerStatusWhenItWorksHasNoStuck(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalIndexerUnit: "active"})
	stubIndexerStatus(t, `{"cursor":900,"tip":900}`, nil)
	if g := collectGlobal(); g == nil || g.Indexer == nil || g.Indexer.Stuck != nil || g.Indexer.Cursor != 900 {
		t.Fatalf("got %+v", g)
	}
}

func TestCollectGlobal_indexerStatusErrorsAreInTheSection(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalIndexerUnit: "active"})
	stubIndexerStatus(t, "", errors.New("connection refused"))
	if g := collectGlobal(); g == nil || g.Indexer == nil || g.Indexer.Error == "" {
		t.Fatalf("an indexer that does not answer must be an error in the section: %+v", g)
	}
	stubIndexerStatus(t, "not json", nil)
	if g := collectGlobal(); g.Indexer == nil || g.Indexer.Error == "" {
		t.Fatalf("a body that is not JSON must be an error in the section: %+v", g.Indexer)
	}
}

func TestCollectGlobal_aStoppedIndexerIsNotAskedForStatus(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalIndexerUnit: "failed"})
	stubIndexerStatus(t, `{}`, errors.New("must not be asked"))
	if g := collectGlobal(); g == nil || g.Indexer != nil || len(g.Units) != 1 || g.Units[0].State != "failed" {
		t.Fatalf("got %+v", g)
	}
}
