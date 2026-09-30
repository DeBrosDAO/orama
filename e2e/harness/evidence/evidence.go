// Package evidence records what a test did against the fleet — every CLI
// invocation, HTTP exchange and SSH command — so a failure in the report comes
// with the requests, responses and commands that led to it.
//
// Each feature package appends to its own JSON-lines file under
// <artifact dir>/evidence/<feature>.jsonl. Every text field is redacted and
// bounded before it is written.
package evidence

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// Kinds of evidence.
const (
	KindCLI  = "cli"
	KindHTTP = "http"
	KindSSH  = "ssh"
)

// MaxFieldBytes bounds each of a record's Input and Output. The head of a
// response is what explains a failure; megabytes of it bloat the report.
const MaxFieldBytes = 16 * 1024

// DirName is the evidence directory under the run's artifact directory.
const DirName = "evidence"

const fileSuffix = ".jsonl"

// truncatedMarker ends a field that was cut at MaxFieldBytes.
const truncatedMarker = "\n…[truncated]"

// Record is one thing a test did.
type Record struct {
	Kind    string `json:"kind"`
	Feature string `json:"feature"`
	Test    string `json:"test"`
	// Seq orders records within one feature's file.
	Seq int `json:"seq"`
	// Summary is one line: "orama version", "GET https://.../health", "node-1: ss -ltnup".
	Summary string `json:"summary"`
	// Status is the exit code of a command or the HTTP status of a response.
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
}

// Recorder appends records to one feature's evidence file. A nil *Recorder
// records nothing: unit tests of the harness construct clients without one.
type Recorder struct {
	mu      sync.Mutex
	path    string
	feature string
	red     *secrets.Redactor
	seq     int
}

// New returns a recorder writing to <dir>/<feature>.jsonl, creating dir.
func New(dir, feature string, red *secrets.Redactor) (*Recorder, error) {
	if feature == "" || strings.ContainsAny(feature, `/\`) {
		return nil, fmt.Errorf("invalid evidence feature name %q", feature)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create evidence dir %s: %w", dir, err)
	}
	if red == nil {
		red = secrets.NewRedactor()
	}
	return &Recorder{path: filepath.Join(dir, feature+fileSuffix), feature: feature, red: red}, nil
}

// Redactor returns the redactor the recorder applies, so callers can register
// values (a freshly minted token) that must never reach a file. It is nil only
// for a nil recorder.
func (r *Recorder) Redactor() *secrets.Redactor {
	if r == nil {
		return nil
	}
	return r.red
}

// Add redacts, bounds and appends rec.
func (r *Recorder) Add(rec Record) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	rec.Seq = r.seq
	rec.Feature = r.feature
	rec.Summary = r.red.Redact(rec.Summary)
	rec.Error = bound(r.red.Redact(rec.Error))
	rec.Input = bound(r.red.Redact(rec.Input))
	rec.Output = bound(r.red.Redact(rec.Output))

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to encode evidence record: %w", err)
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open evidence file %s: %w", r.path, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write evidence file %s: %w", r.path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close evidence file %s: %w", r.path, err)
	}
	return nil
}

// bound cuts s at MaxFieldBytes on a rune boundary.
func bound(s string) string {
	if len(s) <= MaxFieldBytes {
		return s
	}
	cut := MaxFieldBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + truncatedMarker
}

// Load reads every evidence file in dir, ordered by feature then Seq. A missing
// dir is no evidence, not an error: a run that failed before any test ran has none.
func Load(dir string) ([]Record, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+fileSuffix))
	if err != nil {
		return nil, fmt.Errorf("failed to list evidence in %s: %w", dir, err)
	}
	var out []Record
	for _, path := range matches {
		recs, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Feature != out[j].Feature {
			return out[i].Feature < out[j].Feature
		}
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func loadFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open evidence file %s: %w", path, err)
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*MaxFieldBytes+64*1024)
	for line := 1; sc.Scan(); line++ {
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("failed to parse evidence %s line %d: %w", path, line, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read evidence file %s: %w", path, err)
	}
	return out, nil
}
