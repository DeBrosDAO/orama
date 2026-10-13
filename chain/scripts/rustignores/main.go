// Command rustignores keeps the ignored RustSec advisories honest (`make -C chain rust-ignores`).
//
// An advisory is ignored in two places that must agree, deny.toml and RUST_AUDIT_IGNORE in ci.mk, and an
// ignore left alone outlives its reason. The command fails when
//
//   - an ignored id in deny.toml has no "review-by YYYY-MM-DD" in its reason, or that date has passed;
//   - the ids in deny.toml and in RUST_AUDIT_IGNORE differ;
//   - an ignored id matches no crate in any lockfile: cargo-deny reported "advisory-not-detected" for it
//     in every crate it checked, so the ignore covers nothing and is to be removed.
//
// The last check reads the JSON diagnostics that `cargo deny --format json check advisories` writes to
// stderr, one file per crate:
//
//	rustignores -deny deny.toml -ci ci.mk deny-native.json deny-orchardffi.json ...
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	reviewByLayout = "2006-01-02"
	notDetected    = "advisory-not-detected"
)

var (
	ignoreEntryRE = regexp.MustCompile(`^\s*\{\s*id\s*=\s*"(RUSTSEC-\d{4}-\d{4})"\s*,\s*reason\s*=\s*"(.*)"\s*\}\s*,?\s*$`)
	reviewByRE    = regexp.MustCompile(`review-by (\d{4}-\d{2}-\d{2})`)
	auditIgnoreRE = regexp.MustCompile(`(?m)^RUST_AUDIT_IGNORE\s*:=((?:.*\\\n)*.*)$`)
	advisoryIDRE  = regexp.MustCompile(`RUSTSEC-\d{4}-\d{4}`)
)

// ignore is one ignored advisory of deny.toml.
type ignore struct {
	ID     string
	Reason string
}

func main() {
	denyPath := flag.String("deny", "deny.toml", "the cargo-deny policy")
	ciPath := flag.String("ci", "ci.mk", "the makefile that holds RUST_AUDIT_IGNORE")
	todayFlag := flag.String("today", "", "the date to compare review-by dates with, YYYY-MM-DD (default: today, UTC)")
	flag.Parse()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if *todayFlag != "" {
		t, err := time.Parse(reviewByLayout, *todayFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rustignores: -today %q is not YYYY-MM-DD\n", *todayFlag)
			os.Exit(2)
		}
		today = t
	}
	if err := check(*denyPath, *ciPath, flag.Args(), today); err != nil {
		fmt.Fprintln(os.Stderr, "rustignores:", err)
		os.Exit(1)
	}
	fmt.Println("rustignores: every ignored advisory has a current review-by date, matches a crate and is in both lists")
}

func check(denyPath, ciPath string, denyOutputs []string, today time.Time) error {
	denyText, err := os.ReadFile(denyPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", denyPath, err)
	}
	ciText, err := os.ReadFile(ciPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", ciPath, err)
	}
	ignores, err := parseDenyIgnores(string(denyText))
	if err != nil {
		return err
	}
	var problems []error
	problems = append(problems, checkReviewDates(ignores, today)...)
	problems = append(problems, checkListsAgree(ignores, string(ciText))...)

	if len(denyOutputs) == 0 {
		problems = append(problems, errors.New("no cargo-deny output was given: the check that every ignored id matches a crate did not run"))
	} else {
		unused, err := unusedIgnores(ignores, denyOutputs)
		if err != nil {
			return err
		}
		for _, id := range unused {
			problems = append(problems, fmt.Errorf("%s matches no crate in any lockfile (cargo-deny: advisory-not-detected in every crate): remove it from deny.toml and RUST_AUDIT_IGNORE", id))
		}
	}
	return errors.Join(problems...)
}

// parseDenyIgnores returns the ignored advisories of deny.toml's [advisories] ignore list.
func parseDenyIgnores(text string) ([]ignore, error) {
	var out []ignore
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.Contains(line, "RUSTSEC-") {
			continue
		}
		m := ignoreEntryRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("deny.toml: cannot read the ignore entry %q (expected { id = \"RUSTSEC-YYYY-NNNN\", reason = \"...\" })", strings.TrimSpace(line))
		}
		out = append(out, ignore{ID: m[1], Reason: m[2]})
	}
	if len(out) == 0 {
		return nil, errors.New("deny.toml: no ignored advisory found; the parser and the file have drifted apart")
	}
	return out, nil
}

func checkReviewDates(ignores []ignore, today time.Time) []error {
	var problems []error
	for _, ig := range ignores {
		m := reviewByRE.FindStringSubmatch(ig.Reason)
		if m == nil {
			problems = append(problems, fmt.Errorf("%s has no \"review-by YYYY-MM-DD\" in its reason", ig.ID))
			continue
		}
		due, err := time.Parse(reviewByLayout, m[1])
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: review-by %s is not a date", ig.ID, m[1]))
			continue
		}
		if due.Before(today) {
			problems = append(problems, fmt.Errorf("%s: review-by %s has passed (today is %s): look at the advisory again, then move the date or remove the ignore", ig.ID, m[1], today.Format(reviewByLayout)))
		}
	}
	return problems
}

// checkListsAgree compares deny.toml's ids with RUST_AUDIT_IGNORE in ci.mk.
func checkListsAgree(ignores []ignore, ciText string) []error {
	m := auditIgnoreRE.FindStringSubmatch(ciText)
	if m == nil {
		return []error{errors.New("ci.mk: RUST_AUDIT_IGNORE not found")}
	}
	inCI := map[string]bool{}
	for _, id := range advisoryIDRE.FindAllString(m[1], -1) {
		inCI[id] = true
	}
	inDeny := map[string]bool{}
	for _, ig := range ignores {
		inDeny[ig.ID] = true
	}
	var problems []error
	for _, id := range sortedKeys(inDeny) {
		if !inCI[id] {
			problems = append(problems, fmt.Errorf("%s is ignored in deny.toml but not in RUST_AUDIT_IGNORE (ci.mk)", id))
		}
	}
	for _, id := range sortedKeys(inCI) {
		if !inDeny[id] {
			problems = append(problems, fmt.Errorf("%s is in RUST_AUDIT_IGNORE (ci.mk) but not ignored in deny.toml", id))
		}
	}
	return problems
}

// unusedIgnores returns the ignored ids that every cargo-deny output reports as not detected.
func unusedIgnores(ignores []ignore, outputs []string) ([]string, error) {
	notSeen := map[string]int{}
	for _, path := range outputs {
		ids, err := notDetectedIn(path)
		if err != nil {
			return nil, err
		}
		for id := range ids {
			notSeen[id]++
		}
	}
	var unused []string
	for _, ig := range ignores {
		if notSeen[ig.ID] == len(outputs) {
			unused = append(unused, ig.ID)
		}
	}
	sort.Strings(unused)
	return unused, nil
}

// notDetectedIn reads one `cargo deny --format json` stderr stream and returns the advisory ids it
// reports as "advisory-not-detected".
func notDetectedIn(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open cargo-deny output %s: %w", path, err)
	}
	defer f.Close()

	type label struct {
		Span string `json:"span"`
	}
	type line struct {
		Type   string `json:"type"`
		Fields struct {
			Code    string  `json:"code"`
			Labels  []label `json:"labels"`
			Level   string  `json:"level"`
			Message string  `json:"message"`
		} `json:"fields"`
	}
	ids := map[string]bool{}
	sawSummary := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("%s: not cargo-deny JSON: %w", path, err)
		}
		switch {
		case l.Type == "summary":
			sawSummary = true
		case l.Type == "log" && l.Fields.Level == "ERROR":
			return nil, fmt.Errorf("%s: cargo-deny failed: %s", path, l.Fields.Message)
		case l.Type == "diagnostic" && l.Fields.Code == notDetected:
			for _, lb := range l.Fields.Labels {
				if advisoryIDRE.MatchString(lb.Span) {
					ids[lb.Span] = true
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if !sawSummary {
		return nil, fmt.Errorf("%s: no cargo-deny summary line: the run did not finish", path)
	}
	return ids, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
