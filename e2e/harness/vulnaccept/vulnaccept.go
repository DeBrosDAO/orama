// Package vulnaccept judges govulncheck findings against a checked-in list of
// accepted vulnerabilities, each with a reason and a review date.
package vulnaccept

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// dateLayout is the layout of review_by.
	dateLayout = "2006-01-02"
	// maxReviewHorizon is how far past the day it is checked an acceptance may
	// run before someone has to look at it again.
	maxReviewHorizon = 90 * 24 * time.Hour
)

// vulnID is the shape of a Go vulnerability database identifier.
var vulnID = regexp.MustCompile(`^GO-\d{4}-\d{4,}$`)

// Vuln is one vulnerability accepted in one module.
type Vuln struct {
	Module   string `yaml:"module"`
	ID       string `yaml:"id"`
	Reason   string `yaml:"reason"`
	ReviewBy string `yaml:"review_by"`
}

// reviewDate is review_by as a time; Parse has already checked it.
func (a Vuln) reviewDate() time.Time {
	d, _ := time.Parse(dateLayout, a.ReviewBy)
	return d
}

// Parse reads the accepted list and refuses an entry that is
// incomplete, has a malformed ID or date, is listed twice, or whose review_by
// is more than 90 days after now.
func Parse(data []byte, now time.Time) ([]Vuln, error) {
	var file struct {
		Accepted []Vuln `yaml:"accepted"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("govulncheck-accepted.yaml: %w", err)
	}
	seen := map[string]bool{}
	for i, a := range file.Accepted {
		where := fmt.Sprintf("govulncheck-accepted.yaml entry %d (%s %s)", i+1, a.Module, a.ID)
		if a.Module == "" || strings.TrimSpace(a.Reason) == "" {
			return nil, fmt.Errorf("%s: module and reason are required", where)
		}
		if !vulnID.MatchString(a.ID) {
			return nil, fmt.Errorf("%s: id is not a GO-YYYY-NNNN identifier", where)
		}
		d, err := time.Parse(dateLayout, a.ReviewBy)
		if err != nil {
			return nil, fmt.Errorf("%s: review_by %q is not a YYYY-MM-DD date", where, a.ReviewBy)
		}
		if d.After(now.Add(maxReviewHorizon)) {
			return nil, fmt.Errorf("%s: review_by %s is more than 90 days away", where, a.ReviewBy)
		}
		key := a.Module + " " + a.ID
		if seen[key] {
			return nil, fmt.Errorf("%s: listed twice", where)
		}
		seen[key] = true
	}
	return file.Accepted, nil
}

// Called returns the sorted, distinct IDs of the vulnerabilities in the
// output of `govulncheck -format json` whose vulnerable function the code
// reaches. A finding without a function is only an import or a required
// module, which govulncheck's text mode does not count as affecting the code.
func Called(output []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(output))
	ids := map[string]bool{}
	sawConfig := false
	for {
		var msg struct {
			Config  *json.RawMessage `json:"config"`
			Finding *struct {
				OSV   string `json:"osv"`
				Trace []struct {
					Function string `json:"function"`
				} `json:"trace"`
			} `json:"finding"`
		}
		err := dec.Decode(&msg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("govulncheck -format json output: %w", err)
		}
		if msg.Config != nil {
			sawConfig = true
		}
		if f := msg.Finding; f != nil && len(f.Trace) > 0 && f.Trace[0].Function != "" {
			ids[f.OSV] = true
		}
	}
	if !sawConfig {
		return nil, errors.New("govulncheck -format json output has no config message: not a govulncheck run")
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// Judge compares what govulncheck reached in module with the accepted
// list and returns one problem per unlisted finding, per stale entry (listed
// but no longer reported: remove it) and per entry past its review_by.
func Judge(module string, found []string, accepted []Vuln, now time.Time) []string {
	listed := map[string]Vuln{}
	for _, a := range accepted {
		if a.Module == module {
			listed[a.ID] = a
		}
	}
	var problems []string
	reported := map[string]bool{}
	for _, id := range found {
		reported[id] = true
		a, ok := listed[id]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %s is reachable and not accepted: upgrade, or add it to govulncheck-accepted.yaml with a reason", module, id))
		case a.reviewDate().Before(now.Truncate(24 * time.Hour)):
			problems = append(problems, fmt.Sprintf("%s: the acceptance of %s expired on %s: fix it or review the reason and set a new review_by", module, id, a.ReviewBy))
		}
	}
	var ids []string
	for id := range listed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !reported[id] {
			problems = append(problems, fmt.Sprintf("%s: %s is accepted but govulncheck no longer reports it: remove it from govulncheck-accepted.yaml", module, id))
		}
	}
	return problems
}
