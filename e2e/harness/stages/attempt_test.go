package stages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// evidenceExec writes one evidence line per run into the directory the
// runner hands the package, and prints a secret on stdout and stderr.
func evidenceExec(t *testing.T, line string) Executor {
	return func(_ context.Context, c Command) (int, error) {
		var dir string
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "E2E_EVIDENCE_DIR="); ok {
				dir = v
			}
		}
		if dir == "" {
			t.Error("no E2E_EVIDENCE_DIR in the package environment")
			return 1, nil
		}
		f, err := os.OpenFile(filepath.Join(dir, "a.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return -1, err
		}
		fmt.Fprintln(f, line)
		if err := f.Close(); err != nil {
			return -1, err
		}
		fmt.Fprintf(c.Stdout, `{"Action":"output","Package":"p","Test":"TestX","Output":"minted-secret-777 {\"api_key\":\"ak-1234567\"}\n"}`+"\n")
		fmt.Fprintln(c.Stderr, "stderr minted-secret-777")
		return 0, nil
	}
}

// TestRun_rerunReplacesEvidence: running a stage again (--stage, --resume)
// gives the package a fresh evidence dir, so the report never mixes records
// of two attempts.
func TestRun_rerunReplacesEvidence(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}})
	r.Exec = evidenceExec(t, `{"seq":1,"attempt":"first"}`)
	if _, err := r.Run(context.Background(), steps, Options{Only: 1}); err != nil {
		t.Fatal(err)
	}
	r.Exec = evidenceExec(t, `{"seq":1,"attempt":"second"}`)
	tl, err := r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil {
		t.Fatal(err)
	}
	pr := tl.Stages[0].Packages[0]
	if pr.Evidence == "" {
		t.Fatal("the package run does not name its evidence dir")
	}
	raw, err := os.ReadFile(filepath.Join(r.ArtifactDir, pr.Evidence, "a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "first") || !strings.Contains(string(raw), "second") {
		t.Fatalf("evidence of two attempts:\n%s", raw)
	}
}

func TestRun_outputsRedactedWithRunRedactor(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	r.Exec = evidenceExec(t, "{}")
	r.Redactor = func() (*secrets.Redactor, error) { return secrets.NewRedactor("minted-secret-777"), nil }
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}})
	tl, err := r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil {
		t.Fatal(err)
	}
	pr := tl.Stages[0].Packages[0]
	for _, p := range []string{pr.Output, pr.Output + StderrSuffix} {
		raw, err := os.ReadFile(filepath.Join(r.ArtifactDir, p))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "minted-secret-777") || strings.Contains(string(raw), "ak-1234567") {
			t.Fatalf("%s not redacted:\n%s", p, raw)
		}
	}
	r.Redactor = func() (*secrets.Redactor, error) { return nil, errors.New("registry unreadable") }
	tl, err = r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil {
		t.Fatal(err)
	}
	pr = tl.Stages[0].Packages[0]
	if !strings.Contains(pr.Error, "registry unreadable") {
		t.Fatalf("a redaction failure was not recorded: %+v", pr)
	}
	// Fail closed: the unredacted output is withheld, not left behind.
	for _, p := range []string{pr.Output, pr.Output + StderrSuffix} {
		raw, err := os.ReadFile(filepath.Join(r.ArtifactDir, p))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(raw)) != secrets.Withheld {
			t.Fatalf("%s after a redactor failure:\n%s", p, raw)
		}
	}
	// The next package loads the redactor afresh and redacts again.
	r.Redactor = func() (*secrets.Redactor, error) { return secrets.NewRedactor("minted-secret-777"), nil }
	tl, err = r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil || tl.Stages[0].Packages[0].Error != "" {
		t.Fatalf("a later package stayed withheld: %v %+v", err, tl.Stages[0].Packages[0])
	}
}
