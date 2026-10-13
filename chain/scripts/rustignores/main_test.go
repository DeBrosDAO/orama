package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

const denyTwo = `[advisories]
ignore = [
    # a comment that names RUSTSEC-2026-9999 is not an entry
    { id = "RUSTSEC-2026-0001", reason = "rkyv: not reachable (review-by 2027-01-12)" },
    { id = "RUSTSEC-2026-0002", reason = "tar: build only (review-by 2027-01-12)" },
]
`

const ciTwo = `RUST_TOOLCHAIN ?= 1.98.1
RUST_AUDIT_IGNORE := RUSTSEC-2026-0001 \
	RUSTSEC-2026-0002

SEMGREP_SCOPE := app
`

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// denyJSON is what `cargo deny --format json check advisories` writes to stderr: one diagnostic per
// ignored id the crate does not have, then a summary.
func denyJSON(notDetected ...string) string {
	var b strings.Builder
	for _, id := range notDetected {
		b.WriteString(`{"fields":{"code":"advisory-not-detected","labels":[{"message":"no crate matched advisory criteria","span":"` + id + `"},{"message":"ignore reason","span":"some reason"}],"message":"advisory was not encountered","severity":"warning"},"type":"diagnostic"}` + "\n")
	}
	b.WriteString(`{"fields":{"advisories":{"errors":0,"helps":0,"notes":3,"warnings":1}},"type":"summary"}` + "\n")
	return b.String()
}

func TestCheck_cleanPolicyPasses(t *testing.T) {
	deny, ci := write(t, "deny.toml", denyTwo), write(t, "ci.mk", ciTwo)
	a := write(t, "a.json", denyJSON("RUSTSEC-2026-0002")) // only crate A lacks 0002
	b := write(t, "b.json", denyJSON("RUSTSEC-2026-0001"))
	if err := check(deny, ci, []string{a, b}, today); err != nil {
		t.Fatalf("clean policy refused: %v", err)
	}
}

func TestCheck_ignoreThatMatchesNothingInAnyLockfileFails(t *testing.T) {
	deny, ci := write(t, "deny.toml", denyTwo), write(t, "ci.mk", ciTwo)
	a := write(t, "a.json", denyJSON("RUSTSEC-2026-0002"))
	b := write(t, "b.json", denyJSON("RUSTSEC-2026-0002", "RUSTSEC-2026-0001"))
	err := check(deny, ci, []string{a, b}, today)
	if err == nil || !strings.Contains(err.Error(), "RUSTSEC-2026-0002 matches no crate") {
		t.Fatalf("an ignore that no crate has was accepted: %v", err)
	}
	if strings.Contains(err.Error(), "RUSTSEC-2026-0001 matches") {
		t.Fatalf("an ignore that one crate has was reported unused: %v", err)
	}
}

func TestCheck_passedReviewDateFails(t *testing.T) {
	deny, ci := write(t, "deny.toml", denyTwo), write(t, "ci.mk", ciTwo)
	a := write(t, "a.json", denyJSON())
	late := time.Date(2027, 1, 13, 0, 0, 0, 0, time.UTC)
	err := check(deny, ci, []string{a}, late)
	if err == nil || !strings.Contains(err.Error(), "review-by 2027-01-12 has passed") {
		t.Fatalf("a passed review date was accepted: %v", err)
	}
	if err := check(deny, ci, []string{a}, time.Date(2027, 1, 12, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("the review day itself must still pass: %v", err)
	}
}

func TestCheck_missingReviewDateFails(t *testing.T) {
	deny := write(t, "deny.toml", strings.Replace(denyTwo, " (review-by 2027-01-12)", "", 1))
	ci := write(t, "ci.mk", ciTwo)
	a := write(t, "a.json", denyJSON())
	err := check(deny, ci, []string{a}, today)
	if err == nil || !strings.Contains(err.Error(), "RUSTSEC-2026-0001 has no \"review-by") {
		t.Fatalf("an ignore without a review date was accepted: %v", err)
	}
}

func TestCheck_listsThatDisagreeFail(t *testing.T) {
	deny := write(t, "deny.toml", denyTwo)
	ci := write(t, "ci.mk", strings.Replace(ciTwo, "RUSTSEC-2026-0002", "RUSTSEC-2026-0003", 1))
	a := write(t, "a.json", denyJSON())
	err := check(deny, ci, []string{a}, today)
	if err == nil {
		t.Fatal("lists that disagree were accepted")
	}
	for _, want := range []string{"RUSTSEC-2026-0002 is ignored in deny.toml but not in RUST_AUDIT_IGNORE", "RUSTSEC-2026-0003 is in RUST_AUDIT_IGNORE (ci.mk) but not ignored in deny.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

func TestCheck_noCargoDenyOutputFails(t *testing.T) {
	deny, ci := write(t, "deny.toml", denyTwo), write(t, "ci.mk", ciTwo)
	if err := check(deny, ci, nil, today); err == nil {
		t.Fatal("a run with no cargo-deny output was accepted: the unused-ignore check did not run")
	}
}

func TestNotDetectedIn_refusesAFailedOrTruncatedRun(t *testing.T) {
	failed := write(t, "failed.json", `{"fields":{"level":"ERROR","message":"failed to fetch crates"},"type":"log"}`+"\n")
	if _, err := notDetectedIn(failed); err == nil || !strings.Contains(err.Error(), "cargo-deny failed") {
		t.Fatalf("a failed cargo-deny run was read as clean: %v", err)
	}
	truncated := write(t, "truncated.json", `{"fields":{"code":"advisory-not-detected","labels":[{"span":"RUSTSEC-2026-0001"}]},"type":"diagnostic"}`+"\n")
	if _, err := notDetectedIn(truncated); err == nil || !strings.Contains(err.Error(), "no cargo-deny summary") {
		t.Fatalf("a run without a summary was read as finished: %v", err)
	}
}

// The real files of the repository satisfy the rules that need no cargo-deny run.
func TestRepository_denyTomlAndCiMkAgreeAndAreReviewed(t *testing.T) {
	denyText, err := os.ReadFile("../../deny.toml")
	if err != nil {
		t.Fatal(err)
	}
	ciText, err := os.ReadFile("../../ci.mk")
	if err != nil {
		t.Fatal(err)
	}
	ignores, err := parseDenyIgnores(string(denyText))
	if err != nil {
		t.Fatal(err)
	}
	if len(ignores) == 0 {
		t.Fatal("deny.toml ignores nothing")
	}
	for _, p := range checkListsAgree(ignores, string(ciText)) {
		t.Error(p)
	}
	// A review-by in the past fails the build on the day it passes; the test checks only the form.
	for _, p := range checkReviewDates(ignores, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error(p)
	}
}
