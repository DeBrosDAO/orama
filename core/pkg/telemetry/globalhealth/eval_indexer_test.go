package globalhealth

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func indexerNode(state string, ix *report.IndexerReport) Node {
	return Node{Host: "a", Global: &report.GlobalReport{
		Units:   []report.GlobalUnit{{Name: constants.GlobalIndexerUnit, State: state}},
		Indexer: ix,
	}}
}

func TestEvaluate_stuckIndexerIsReportedWithItsReason(t *testing.T) {
	ix := &report.IndexerReport{Cursor: 40, Tip: 900, Stuck: &report.IndexerStuck{Height: 41, Reason: "the node pruned the state", StatePruned: true}}
	got := issuesFor(t, indexerNode("active", ix))
	if len(got) != 1 || got[0].Code != "global.indexer.stuck" || got[0].Severity != Warning {
		t.Fatalf("got %+v", got)
	}
	for _, want := range []string{"block 41", "40 of 900", "the node pruned the state"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q is missing %q", got[0].Message, want)
		}
	}
}

func TestEvaluate_workingIndexerRaisesNothing(t *testing.T) {
	if got := issuesFor(t, indexerNode("active", &report.IndexerReport{Cursor: 900, Tip: 900})); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_indexerThatDoesNotAnswerOrIsDown(t *testing.T) {
	got := issuesFor(t, indexerNode("active", nil))
	if len(got) != 1 || got[0].Code != "global.indexer.api" {
		t.Fatalf("no section: %+v", got)
	}
	got = issuesFor(t, indexerNode("active", &report.IndexerReport{Error: "connection refused"}))
	if len(got) != 1 || got[0].Code != "global.indexer.api" || !strings.Contains(got[0].Message, "connection refused") {
		t.Fatalf("an error: %+v", got)
	}
	got = issuesFor(t, indexerNode("failed", nil))
	if len(got) != 1 || got[0].Code != "global.indexer.down" || got[0].Severity != Critical {
		t.Fatalf("a failed unit: %+v", got)
	}
}
