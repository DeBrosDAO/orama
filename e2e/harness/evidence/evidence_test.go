package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

func TestAdd_roundTripRedactedAndOrdered(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, "smoke", secrets.NewRedactor("very-secret-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Record{Kind: KindCLI, Test: "TestA", Summary: "orama version", Output: "uses very-secret-token"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Record{Kind: KindHTTP, Test: "TestB", Summary: "GET /health", Status: 200}); err != nil {
		t.Fatal(err)
	}
	recs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Seq != 1 || recs[1].Seq != 2 || recs[0].Feature != "smoke" {
		t.Fatalf("unexpected records: %+v", recs)
	}
	if strings.Contains(recs[0].Output, "very-secret-token") {
		t.Fatal("secret reached the evidence file")
	}
	info, err := os.Stat(filepath.Join(dir, "smoke.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence file mode %o, want 600", info.Mode().Perm())
	}
}

func TestAdd_boundsHugeOutput(t *testing.T) {
	r, err := New(t.TempDir(), "big", nil)
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("é", MaxFieldBytes) // two bytes per rune
	if err := r.Add(Record{Kind: KindSSH, Output: huge}); err != nil {
		t.Fatal(err)
	}
	recs, err := Load(filepath.Dir(r.path))
	if err != nil {
		t.Fatal(err)
	}
	out := recs[0].Output
	if len(out) > MaxFieldBytes+len(truncatedMarker) || !strings.HasSuffix(out, truncatedMarker) {
		t.Fatalf("output not bounded: len=%d", len(out))
	}
	if !strings.HasPrefix(out, "é") || strings.ContainsRune(out, '�') {
		t.Fatal("output cut inside a rune")
	}
}

func TestAdd_nilRecorderIsNoop(t *testing.T) {
	var r *Recorder
	if err := r.Add(Record{Kind: KindCLI}); err != nil {
		t.Fatalf("nil recorder: %v", err)
	}
	if r.Redactor() != nil {
		t.Fatal("nil recorder has a redactor")
	}
}

func TestAdd_concurrentWritersKeepUniqueSeq(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, "conc", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Add(Record{Kind: KindHTTP}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	recs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, rec := range recs {
		if rec.Seq != i+1 {
			t.Fatalf("record %d has seq %d", i, rec.Seq)
		}
	}
}

func TestNew_rejectsBadFeatureName(t *testing.T) {
	for _, name := range []string{"", "a/b", `a\b`} {
		if _, err := New(t.TempDir(), name, nil); err == nil {
			t.Errorf("feature %q accepted", name)
		}
	}
}

func TestLoad_missingDirAndCorruptFile(t *testing.T) {
	recs, err := Load(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(recs) != 0 {
		t.Fatalf("missing dir: recs=%v err=%v", recs, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt evidence accepted")
	}
}
