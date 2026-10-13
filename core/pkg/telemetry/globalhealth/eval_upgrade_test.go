package globalhealth

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func issueFor(issues []Issue, code string) (Issue, bool) {
	for _, i := range issues {
		if i.Code == code {
			return i, true
		}
	}
	return Issue{}, false
}

func TestEvaluate_aScheduledUpgradeWithoutAStagedBinaryWarns(t *testing.T) {
	c := chain(1000, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 1500}
	issue, ok := issueFor(issuesFor(t, Node{Host: "n1", Chain: c}), "chain.upgrade-unstaged")
	if !ok || issue.Severity != Warning {
		t.Fatalf("issues = %+v, want a chain.upgrade-unstaged warning", issue)
	}
	for _, want := range []string{`"v0-4-0"`, "height 1500", "500 blocks", "not staged", "stage-oramad --upgrade v0-4-0"} {
		if !strings.Contains(issue.Message, want) {
			t.Errorf("message %q lacks %q", issue.Message, want)
		}
	}
}

func TestEvaluate_aStagedUpgradeRaisesNothing(t *testing.T) {
	c := chain(1000, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 1500, Staged: true}
	if issues := issuesFor(t, Node{Host: "n1", Chain: c}); len(issues) != 0 {
		t.Fatalf("a staged upgrade raised %+v", issues)
	}
}

func TestEvaluate_anUpgradeAtOrBelowTheChainHeightIsReportedAsReached(t *testing.T) {
	c := chain(1500, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 1500}
	issue, ok := issueFor(issuesFor(t, Node{Host: "n1", Chain: c}), "chain.upgrade-unstaged")
	if !ok || !strings.Contains(issue.Message, "the chain is at its height 1500") {
		t.Fatalf("got %+v", issue)
	}
}

func TestEvaluate_aStageErrorIsInTheWarning(t *testing.T) {
	c := chain(10, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "Bad Name", Height: 20, StageError: "upgrade name must be lowercase"}
	issue, ok := issueFor(issuesFor(t, Node{Host: "n1", Chain: c}), "chain.upgrade-unstaged")
	if !ok || !strings.Contains(issue.Message, "cannot be staged: upgrade name must be lowercase") {
		t.Fatalf("got %+v", issue)
	}
}

func TestEvaluate_anUnreadablePlanWarns(t *testing.T) {
	c := chain(10, 3, 3)
	c.UpgradeError = "HTTP 500"
	issue, ok := issueFor(issuesFor(t, Node{Host: "n1", Chain: c}), "chain.upgrade-plan")
	if !ok || issue.Severity != Warning || !strings.Contains(issue.Message, "HTTP 500") {
		t.Fatalf("got %+v", issue)
	}
}

func TestEvaluate_aStagedBinaryThatIsNotThePlansIsCritical(t *testing.T) {
	c := chain(1000, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 1500, Staged: true, Checksums: report.ChecksumsMismatch, ChecksumProblem: "the staged oramad is not the one the plan names"}
	issue, ok := issueFor(issuesFor(t, Node{Host: "n1", Chain: c}), "chain.upgrade-mismatch")
	if !ok || issue.Severity != Critical {
		t.Fatalf("issue = %+v, want a critical chain.upgrade-mismatch", issue)
	}
	for _, want := range []string{`"v0-4-0"`, "the staged oramad is not the one the plan names", "stage-oramad --upgrade v0-4-0"} {
		if !strings.Contains(issue.Message, want) {
			t.Errorf("message %q lacks %q", issue.Message, want)
		}
	}
}

func TestEvaluate_aStagedBinaryThatMatchesThePlanRaisesNothing(t *testing.T) {
	c := chain(1000, 3, 3)
	c.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 1500, Staged: true, Checksums: report.ChecksumsMatch}
	if issues := issuesFor(t, Node{Host: "n1", Chain: c}); len(issues) != 0 {
		t.Fatalf("raised %+v", issues)
	}
}
