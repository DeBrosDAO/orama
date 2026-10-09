package vulnaccept

import (
	"os"
	"strings"
	"testing"
	"time"
)

var scanned = []string{"chain", "core", "caddy", "core/thirdparty/ipfs-cluster", "core/thirdparty/olric"}

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const okEntry = `accepted:
  - module: chain
    id: GO-2026-0001
    reason: not reachable
    review_by: 2026-12-01
`

func TestParse_checkedInFileIsValid(t *testing.T) {
	data, err := os.ReadFile("../../features/scanners/govulncheck-accepted.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data, now, scanned); err != nil {
		t.Fatal(err)
	}
}

func TestParse_valid(t *testing.T) {
	got, err := Parse([]byte(okEntry), now, scanned)
	if err != nil || len(got) != 1 || got[0].ID != "GO-2026-0001" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParse_emptyListIsFine(t *testing.T) {
	for _, in := range []string{"", "accepted: []\n"} {
		if got, err := Parse([]byte(in), now, scanned); err != nil || len(got) != 0 {
			t.Errorf("%q: got %v, %v", in, got, err)
		}
	}
}

func TestParse_rejects(t *testing.T) {
	entry := func(mod, id, reason, by string) string {
		return "accepted:\n  - module: " + mod + "\n    id: " + id + "\n    reason: \"" + reason + "\"\n    review_by: " + by + "\n"
	}
	cases := map[string]struct{ in, want string }{
		"no reason":        {entry("chain", "GO-2026-0001", " ", "2026-12-01"), "required"},
		"no module":        {entry("", "GO-2026-0001", "r", "2026-12-01"), "required"},
		"bad id":           {entry("chain", "CVE-2026-1", "r", "2026-12-01"), "GO-YYYY-NNNN"},
		"bad date":         {entry("chain", "GO-2026-0001", "r", "soon"), "YYYY-MM-DD"},
		"no date":          {entry("chain", "GO-2026-0001", "r", "\"\""), "YYYY-MM-DD"},
		"over 90 days":     {entry("chain", "GO-2026-0001", "r", "2027-01-01"), "90 days"},
		"unknown field":    {okEntry + "    note: x\n", "note"},
		"duplicate":        {okEntry + okEntry[len("accepted:\n"):], "twice"},
		"unscanned module": {entry("chian", "GO-2026-0001", "r", "2026-12-01"), "scanned modules"},
		"not yaml":         {"accepted: [", "govulncheck-accepted.yaml"},
	}
	for name, c := range cases {
		_, err := Parse([]byte(c.in), now, scanned)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want it to mention %q", name, err, c.want)
		}
	}
}

func TestParse_exactly90DaysIsAllowed(t *testing.T) {
	in := strings.Replace(okEntry, "2026-12-01", now.Add(maxReviewHorizon).Format(dateLayout), 1)
	if _, err := Parse([]byte(in), now, scanned); err != nil {
		t.Fatal(err)
	}
}

const configLine = `{"config":{"protocol_version":"v1.0.0"}}` + "\n"

func TestCalled_countsOnlyReachedFunctions(t *testing.T) {
	out := configLine +
		`{"osv":{"id":"GO-2026-0002"}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0002","trace":[{"module":"m","function":"F","package":"p"},{"function":"main"}]}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0002","trace":[{"module":"m","function":"G"}]}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0001","trace":[{"module":"m","function":"G"}]}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0003","trace":[{"module":"m","package":"p"}]}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0004","trace":[{"module":"m"}]}}` + "\n" +
		`{"finding":{"osv":"GO-2026-0005","trace":[]}}` + "\n"
	got, err := Called([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "GO-2026-0001,GO-2026-0002" {
		t.Errorf("got %v", got)
	}
}

func TestCalled_cleanRun(t *testing.T) {
	got, err := Called([]byte(configLine + `{"progress":{"message":"x"}}` + "\n"))
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestCalled_notGovulncheckOutput(t *testing.T) {
	for _, in := range []string{"", "package requires newer Go version go1.27\n", `{"finding":{"osv":"GO-2026-0001","trace":[{"function":"F"}]}}`} {
		if got, err := Called([]byte(in)); err == nil {
			t.Errorf("%q: got %v, want an error: a scan that did not run must not read as clean", in, got)
		}
	}
}

func TestJudge_reportsUnlistedStaleAndExpired(t *testing.T) {
	acc := []Vuln{
		{Module: "chain", ID: "GO-2026-0001", ReviewBy: "2026-12-01"},
		{Module: "chain", ID: "GO-2026-0002", ReviewBy: "2026-09-01"},
		{Module: "chain", ID: "GO-2026-0003", ReviewBy: "2026-12-01"},
		{Module: "core", ID: "GO-2026-0009", ReviewBy: "2026-12-01"},
	}
	cases := []struct {
		name   string
		module string
		found  []string
		want   []string // a substring of each problem, in order
	}{
		{"accepted and in date", "chain", []string{"GO-2026-0001", "GO-2026-0003"}, []string{"GO-2026-0002 is accepted but govulncheck no longer reports it"}},
		{"unlisted finding", "chain", []string{"GO-2026-0001", "GO-2026-0003", "GO-2026-0100"}, []string{"GO-2026-0100 is reachable and not accepted", "GO-2026-0002 is accepted"}},
		{"stale entries", "chain", []string{"GO-2026-0001"}, []string{"GO-2026-0002 is accepted", "GO-2026-0003 is accepted"}},
		{"expired entry still reported", "chain", []string{"GO-2026-0001", "GO-2026-0002", "GO-2026-0003"}, []string{"acceptance of GO-2026-0002 expired on 2026-09-01"}},
		{"another module's entry is not this module's", "e2e", nil, nil},
		{"a finding in a module with no entries", "e2e", []string{"GO-2026-0009"}, []string{"GO-2026-0009 is reachable and not accepted"}},
		{"nothing found, all stale", "chain", nil, []string{"GO-2026-0001", "GO-2026-0002", "GO-2026-0003"}},
	}
	for _, c := range cases {
		got := Judge(c.module, c.found, acc, now)
		if len(got) != len(c.want) {
			t.Errorf("%s: %d problems, want %d: %v", c.name, len(got), len(c.want), got)
			continue
		}
		for i := range c.want {
			if !strings.Contains(got[i], c.want[i]) {
				t.Errorf("%s: problem %d is %q, want it to contain %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestJudge_reviewByTodayIsStillInDate(t *testing.T) {
	acc := []Vuln{{Module: "chain", ID: "GO-2026-0001", ReviewBy: "2026-10-01"}}
	if got := Judge("chain", []string{"GO-2026-0001"}, acc, now); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}
