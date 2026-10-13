package nsledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

var (
	t0   = time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	fast = Options{Interval: time.Millisecond, Budget: 200 * time.Millisecond}
)

func ledgerIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), FileName)
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Namespace)
	}
	return out
}

func TestPending_recordedAndNotGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	for i, n := range []string{"e2e-a", "e2e-b", "e2e-c"} {
		if err := Record(dir, n, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkGone(path, "e2e-b"); err != nil {
		t.Fatal(err)
	}
	got, err := Pending(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "e2e-a,e2e-c" || !got[1].At.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("pending %+v, want a and c with their times", got)
	}
}

func TestPending_missingLedgerIsEmpty(t *testing.T) {
	if got, err := Pending(ledgerIn(t)); err != nil || len(got) != 0 {
		t.Fatalf("missing ledger: %v %v", got, err)
	}
}

// TestPending_aTornLineDoesNotHideTheOthers: a kill mid-write leaves half a
// line; the namespaces before it must still be removed, and the line reported.
func TestPending_aTornLineDoesNotHideTheOthers(t *testing.T) {
	path := ledgerIn(t)
	body := "{\"namespace\":\"e2e-a\",\"at\":\"2026-10-01T06:00:00Z\"}\n{\"namespace\":\"e2e-b\",\"at\":\"20"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Pending(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("the bad line was not reported: %v", err)
	}
	if strings.Join(names(got), ",") != "e2e-a" {
		t.Fatalf("entries %+v, want e2e-a", got)
	}
	var removed []string
	remove := func(_ context.Context, n string) error { removed = append(removed, n); return nil }
	if rerr := Reconcile(context.Background(), []string{path}, time.Time{}, remove, fast); rerr == nil || strings.Join(removed, ",") != "e2e-a" {
		t.Fatalf("reconcile removed %v, err %v: want e2e-a removed and the bad line reported", removed, rerr)
	}
}

func TestRecord_refusesANameWithoutThePrefix(t *testing.T) {
	dir := t.TempDir()
	if err := Record(dir, "ab", t0); err == nil {
		t.Fatal("a namespace a test did not generate was recorded")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Fatal("a ledger was written for a refused name")
	}
}

// TestReconcile_aNameWithoutThePrefixNeverReachesTheRemover: a ledger is a
// file; a line in it (hand edited, another tool's) must not be able to make
// the runner remove a real namespace.
func TestReconcile_aNameWithoutThePrefixNeverReachesTheRemover(t *testing.T) {
	path := ledgerIn(t)
	body := "{\"namespace\":\"stagenetproof\",\"at\":\"2026-10-01T06:00:00Z\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	remove := func(_ context.Context, n string) error { t.Errorf("the remover was asked to remove %s", n); return nil }
	err := Reconcile(context.Background(), []string{path}, time.Time{}, remove, fast)
	if !errors.Is(err, ErrNotEphemeral) {
		t.Fatalf("err %v", err)
	}
	if err := RemoveAll(context.Background(), []string{"anchatdemo"}, remove, fast); !errors.Is(err, ErrNotEphemeral) {
		t.Fatalf("RemoveAll err %v", err)
	}
}

func TestReconcile_aPermanentFailureIsNotRetried(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, "e2e-a", t0)
	calls := 0
	remove := func(context.Context, string) error { calls++; return fmt.Errorf("%w: HTTP 403", ErrPermanent) }
	err := Reconcile(context.Background(), []string{filepath.Join(dir, FileName)}, time.Time{}, remove, Options{Interval: time.Millisecond, Budget: time.Minute})
	if err == nil || calls != 1 {
		t.Fatalf("%d calls, err %v: want one call and a failure", calls, err)
	}
}

func TestReconcile_totalBoundsTheWholeCleanup(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"e2e-a", "e2e-b", "e2e-c"} {
		_ = Record(dir, n, t0)
	}
	remove := func(context.Context, string) error { return errors.New("HTTP 500") }
	start := time.Now()
	err := Reconcile(context.Background(), []string{filepath.Join(dir, FileName)}, time.Time{}, remove,
		Options{Interval: time.Millisecond, Budget: time.Minute, Total: 100 * time.Millisecond})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("took %v, err %v: three namespaces with a minute each must stop at the total", time.Since(start), err)
	}
}

func TestPending_goneThenRecordedAgainIsPendingOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = Record(dir, "e2e-a", t0)
	_ = MarkGone(path, "e2e-a")
	_ = Record(dir, "e2e-a", t0.Add(time.Hour))
	got, err := Pending(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("pending %+v %v, want e2e-a once", got, err)
	}
}

func TestRecordFromEnv_onlyWithAnEvidenceDir(t *testing.T) {
	dir := t.TempDir()
	if err := RecordFromEnv(func(string) (string, bool) { return "", false }, "e2e-a", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Fatal("a ledger appeared without an evidence dir")
	}
	lookup := func(k string) (string, bool) { return dir, k == config.EnvEvidenceDir }
	if err := RecordFromEnv(lookup, "e2e-a", t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := Pending(filepath.Join(dir, FileName)); len(got) != 1 {
		t.Fatalf("recorded %+v", got)
	}
}

// TestReconcile_retriesATransientRefusal is the leak: a removal that is
// refused once ("retry shortly") must be tried again, not given up on.
func TestReconcile_retriesATransientRefusal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = Record(dir, "e2e-a", t0)
	calls := 0
	remove := func(context.Context, string) error {
		if calls++; calls < 3 {
			return errors.New("the cluster reference index is not ready; retry shortly")
		}
		return nil
	}
	if err := Reconcile(context.Background(), []string{path}, time.Time{}, remove, fast); err != nil {
		t.Fatal(err)
	}
	if got, _ := Pending(path); len(got) != 0 || calls != 3 {
		t.Fatalf("pending %+v after %d calls", got, calls)
	}
}

func TestReconcile_notFoundMeansGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = Record(dir, "e2e-a", t0)
	remove := func(context.Context, string) error { return ErrNotFound }
	if err := Reconcile(context.Background(), []string{path}, time.Time{}, remove, fast); err != nil {
		t.Fatal(err)
	}
	if got, _ := Pending(path); len(got) != 0 {
		t.Fatalf("a namespace that does not exist stays pending: %+v", got)
	}
}

func TestReconcile_failureIsReportedKeepsTheEntryAndGoesOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = Record(dir, "e2e-stuck", t0)
	_ = Record(dir, "e2e-fine", t0)
	remove := func(_ context.Context, n string) error {
		if n == "e2e-stuck" {
			return errors.New("HTTP 500")
		}
		return nil
	}
	err := Reconcile(context.Background(), []string{path}, time.Time{}, remove, fast)
	if err == nil || !strings.Contains(err.Error(), "e2e-stuck may be leaked") || strings.Contains(err.Error(), "e2e-fine") {
		t.Fatalf("err %v", err)
	}
	if got, _ := Pending(path); strings.Join(names(got), ",") != "e2e-stuck" {
		t.Fatalf("pending %+v, want only e2e-stuck", got)
	}
}

func TestReconcile_cutoffSparesNewerEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = Record(dir, "e2e-old", t0)
	_ = Record(dir, "e2e-new", t0.Add(2*time.Hour))
	var removed []string
	remove := func(_ context.Context, n string) error { removed = append(removed, n); return nil }
	if err := Reconcile(context.Background(), []string{path}, t0.Add(time.Hour), remove, fast); err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != "e2e-old" {
		t.Fatalf("removed %v, want only e2e-old", removed)
	}
}

func TestReconcile_emptyOrMissingLedgerRemovesNothing(t *testing.T) {
	remove := func(context.Context, string) error { t.Fatal("removed something"); return nil }
	if err := Reconcile(context.Background(), []string{ledgerIn(t)}, time.Time{}, remove, fast); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), nil, time.Time{}, remove, fast); err != nil {
		t.Fatal(err)
	}
}

func fakeOrama(t *testing.T, script string) *oramacli.Runner {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "orama")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &oramacli.Runner{Bin: bin, Home: dir, AgentSock: filepath.Join(dir, "a.sock"),
		RealHome: func() (string, error) { return "/nonexistent/home", nil }}
}

func TestOperatorRemover_exitsAndNotFound(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	ok := OperatorRemover(fakeOrama(t, "echo \"$*\" >> "+log+"\n"), "e2e: test")
	if err := ok(context.Background(), "e2e-a"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(raw)); got != "cluster namespace remove e2e-a --reason e2e: test --force" {
		t.Fatalf("called %q", got)
	}
}

// TestOperatorRemover_classifiesByTheHTTPStatus: the CLI exits 1 for a 404 and
// a 500 alike, on stderr or stdout; the status decides what waiting can fix.
func TestOperatorRemover_classifiesByTheHTTPStatus(t *testing.T) {
	cases := []struct {
		name, script string
		notFound     bool
		permanent    bool
		failed       bool
	}{
		{"404 on stderr", "echo 'Error: namespace not found (HTTP 404)' >&2\nexit 1\n", true, false, true},
		{"404 on stdout", "echo 'Error: namespace not found (HTTP 404)'\nexit 1\n", true, false, true},
		{"401", "echo 'Error: not signed in (HTTP 401)' >&2\nexit 5\n", false, true, true},
		{"403", "echo 'Error: not an operator (HTTP 403)' >&2\nexit 1\n", false, true, true},
		{"409 is retried", "echo 'Error: busy (HTTP 409)' >&2\nexit 1\n", false, false, true},
		{"429 is retried", "echo 'Error: slow down (HTTP 429)' >&2\nexit 5\n", false, false, true},
		{"500 is retried", "echo 'Error: could not deprovision (HTTP 500)' >&2\nexit 1\n", false, false, true},
		{"no status is retried", "echo 'Error: TLS handshake timeout' >&2\nexit 5\n", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := OperatorRemover(fakeOrama(t, c.script), "r")(context.Background(), "e2e-a")
			if (err != nil) != c.failed || errors.Is(err, ErrNotFound) != c.notFound || errors.Is(err, ErrPermanent) != c.permanent {
				t.Fatalf("err %v: notFound=%v permanent=%v", err, errors.Is(err, ErrNotFound), errors.Is(err, ErrPermanent))
			}
		})
	}
}
