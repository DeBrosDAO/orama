package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// samplePath is `orama status report --json` as the real code writes it:
// display.FullReport (core/cmd/orama/internal/monitor/display) over a
// snapshot of three healthy nodes with every section this package reads,
// one unreachable node and a warning alert. Regenerate it after a change to
// the report by running a test in that package that writes FullReport's
// output (go test -overlay keeps the generator out of the tree).
var samplePath = filepath.Join("testdata", "monitor-report.json")

func loadSample(t *testing.T) ([]byte, *Report) {
	t.Helper()
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, r
}

// TestDrift_everyFieldExistsInTheRealReport re-encodes what this package
// decoded and requires every key it writes to exist, at the same path, in the
// real report: a field renamed or removed upstream decodes silently as zero,
// and this is where that shows.
func TestDrift_everyFieldExistsInTheRealReport(t *testing.T) {
	raw, r := loadSample(t)
	ours, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var mine, real any
	if err := json.Unmarshal(ours, &mine); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &real); err != nil {
		t.Fatal(err)
	}
	var found []string
	missingKeys("", mine, real, &found)
	seen := map[string]bool{}
	var missing []string
	for _, k := range found {
		if !seen[k] {
			seen[k] = true
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("fields not in the real monitor report (renamed or removed upstream?):\n%s", strings.Join(missing, "\n"))
	}
}

// missingKeys appends the object keys of mine that real does not have.
func missingKeys(path string, mine, real any, out *[]string) {
	switch m := mine.(type) {
	case map[string]any:
		r, ok := real.(map[string]any)
		if !ok {
			*out = append(*out, path+" (not an object upstream)")
			return
		}
		for k, v := range m {
			rv, ok := r[k]
			if !ok {
				*out = append(*out, path+"."+k)
				continue
			}
			missingKeys(path+"."+k, v, rv, out)
		}
	case []any:
		r, _ := real.([]any)
		for i := range m {
			if i < len(r) {
				missingKeys(path+"[]", m[i], r[i], out)
			}
		}
	}
}

// TestDrift_sampleDecodesAndPredicatesRead proves the fields the predicates
// read decode to what the real code wrote.
func TestDrift_sampleDecodesAndPredicatesRead(t *testing.T) {
	_, r := loadSample(t)
	if r.Meta.Environment != "e2e-ab12" || r.Meta.NodeCount != 4 || r.Summary.RQLiteLeader != "1.1.1.1" || r.Summary.WarningAlerts != 1 || len(r.Alerts) != 1 {
		t.Fatalf("envelope %+v %+v", r.Meta, r.Summary)
	}
	n := r.Nodes[0]
	if n.Report == nil || n.Report.RQLite.RaftState != RaftLeader || n.Report.Gateway.HTTPStatus != 200 ||
		len(n.Report.WireGuard.Peers) != 2 || !n.Report.DNS.CoreDNSActive || n.Report.Version != "0.300.0" ||
		n.Report.System.CPUCount != 4 || !n.Report.Network.UFWActive || n.Report.Chain.LatestHeight != 1200 || n.ReportAgeSec != 4 {
		t.Fatalf("node %+v", n.Report)
	}
	if last := r.Nodes[3]; last.Report != nil || !strings.Contains(last.Error, "i/o timeout") {
		t.Fatalf("unreachable node %+v", last)
	}
	if err := r.LeaderAgreement(); err != nil {
		t.Fatal(err)
	}
	if err := r.Converged(4); err == nil || !strings.Contains(err.Error(), "4.4.4.4") {
		t.Fatalf("a report with an unreachable node converged: %v", err)
	}
	if err := r.Serving(); err == nil || !strings.Contains(err.Error(), "4.4.4.4: gateway") {
		t.Fatalf("serving: %v", err)
	}
}
