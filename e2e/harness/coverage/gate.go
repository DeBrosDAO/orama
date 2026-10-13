package coverage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// Status of one universe item.
const (
	StatusCovered   = "covered"
	StatusWaived    = "waived"
	StatusUncovered = "uncovered"
)

// Row is one universe item and what covers it: the coverage matrix the report
// renders.
type Row struct {
	Item
	Status   string   `json:"status"`
	Features []string `json:"features,omitempty"`
	Waiver   *Waiver  `json:"waiver,omitempty"`
}

// Result is the gate's verdict with everything needed to act on it.
type Result struct {
	Rows []Row `json:"rows"`
	// Uncovered are universe ids in no manifest and no waiver.
	Uncovered []string `json:"uncovered"`
	// StaleWaivers are waivers for items a manifest covers, or that no longer exist.
	StaleWaivers []string `json:"stale_waivers"`
	// InvalidWaivers are waivers missing an id, a reason or a trigger, or listed twice.
	InvalidWaivers []string `json:"invalid_waivers"`
	// UnknownCovers are manifest entries of an enumerated kind that match no
	// shipped item: a typo, or a command that was removed.
	UnknownCovers []string `json:"unknown_covers"`
	// Claims and Config are the doc claims and config keys manifests say they
	// test. They have no enumerator; the report lists them.
	Claims map[string][]string `json:"claims"`
	Config map[string][]string `json:"config"`
}

// OK is true when the gate passes.
func (r Result) OK() bool {
	return len(r.Uncovered) == 0 && len(r.StaleWaivers) == 0 &&
		len(r.InvalidWaivers) == 0 && len(r.UnknownCovers) == 0
}

// Counts returns covered, waived and uncovered item counts per kind.
func (r Result) Counts() map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, row := range r.Rows {
		if out[row.Kind] == nil {
			out[row.Kind] = map[string]int{}
		}
		out[row.Kind][row.Status]++
	}
	return out
}

// Evaluate matches the universe against manifests and waivers.
func Evaluate(universe []Item, manifests []manifest.Manifest, waivers []Waiver) Result {
	covers, res := indexCovers(manifests)
	byID := map[string]bool{}
	for _, it := range universe {
		byID[it.ID] = true
	}
	for id, feats := range covers {
		if !byID[id] && enumerated(id) {
			res.UnknownCovers = append(res.UnknownCovers, id+" (in "+strings.Join(feats, ", ")+")")
		}
	}
	waived := indexWaivers(waivers, covers, byID, &res)
	for _, it := range universe {
		row := Row{Item: it, Status: StatusUncovered}
		switch {
		case len(covers[it.ID]) > 0:
			row.Status, row.Features = StatusCovered, covers[it.ID]
		case waived[it.ID] != nil:
			row.Status, row.Waiver = StatusWaived, waived[it.ID]
		default:
			res.Uncovered = append(res.Uncovered, it.ID)
		}
		res.Rows = append(res.Rows, row)
	}
	sort.Slice(res.Rows, func(i, j int) bool { return res.Rows[i].ID < res.Rows[j].ID })
	for _, list := range [][]string{res.Uncovered, res.StaleWaivers, res.InvalidWaivers, res.UnknownCovers} {
		sort.Strings(list)
	}
	return res
}

// enumerated reports whether id is of a kind the universe enumerates.
func enumerated(id string) bool {
	return !strings.HasPrefix(id, manifest.PrefixClaim) && !strings.HasPrefix(id, manifest.PrefixConfig)
}

func indexCovers(manifests []manifest.Manifest) (map[string][]string, Result) {
	covers := map[string][]string{}
	for _, m := range manifests {
		for _, id := range m.Covers.IDs() {
			covers[id] = append(covers[id], m.ID)
		}
	}
	res := Result{Claims: map[string][]string{}, Config: map[string][]string{}}
	for id, feats := range covers {
		sort.Strings(feats)
		switch {
		case strings.HasPrefix(id, manifest.PrefixClaim):
			res.Claims[strings.TrimPrefix(id, manifest.PrefixClaim)] = feats
		case strings.HasPrefix(id, manifest.PrefixConfig):
			res.Config[strings.TrimPrefix(id, manifest.PrefixConfig)] = feats
		}
	}
	return covers, res
}

func indexWaivers(waivers []Waiver, covers map[string][]string, universe map[string]bool, res *Result) map[string]*Waiver {
	out := map[string]*Waiver{}
	for i := range waivers {
		w := &waivers[i]
		switch {
		case strings.TrimSpace(w.ID) == "":
			res.InvalidWaivers = append(res.InvalidWaivers, fmt.Sprintf("waiver #%d has no id", i+1))
			continue
		case strings.TrimSpace(w.Reason) == "" || strings.TrimSpace(w.Trigger) == "":
			res.InvalidWaivers = append(res.InvalidWaivers, w.ID+": needs both a reason and a trigger")
		case out[w.ID] != nil:
			res.InvalidWaivers = append(res.InvalidWaivers, w.ID+": waived twice")
		}
		switch {
		case len(covers[w.ID]) > 0:
			res.StaleWaivers = append(res.StaleWaivers, w.ID+": covered by "+strings.Join(covers[w.ID], ", ")+"; delete the waiver")
		case !universe[w.ID]:
			res.StaleWaivers = append(res.StaleWaivers, w.ID+": no longer shipped; delete the waiver")
		}
		out[w.ID] = w
	}
	return out
}

// Format renders the gate's failures as a sorted, actionable list; empty when OK.
func (r Result) Format() string {
	var b strings.Builder
	section := func(title, hint string, ids []string) {
		if len(ids) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%d) — %s\n", title, len(ids), hint)
		for _, id := range ids {
			fmt.Fprintf(&b, "  - %s\n", id)
		}
	}
	section("UNCOVERED", "add a features/<x> test and list it under covers, or waive it in e2e/waivers.yaml with a reason and a trigger", r.Uncovered)
	section("UNKNOWN COVERS", "fix the manifest entry: it matches nothing that ships", r.UnknownCovers)
	section("STALE WAIVERS", "remove them from e2e/waivers.yaml", r.StaleWaivers)
	section("INVALID WAIVERS", "every waiver needs id, reason and trigger", r.InvalidWaivers)
	return b.String()
}

// Summary is one line: per-kind covered/waived/total.
func (r Result) Summary() string {
	counts := r.Counts()
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		c := counts[k]
		total := c[StatusCovered] + c[StatusWaived] + c[StatusUncovered]
		parts = append(parts, fmt.Sprintf("%s %d covered, %d waived of %d", k, c[StatusCovered], c[StatusWaived], total))
	}
	return strings.Join(parts, "; ")
}
