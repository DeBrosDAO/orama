package printer

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Seven copies of FormatBytes existed and they did not agree: two rendered
// "1.0KB" where the rest rendered "1.0 KB" for the same number.
func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{500, "500 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	} {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A buffer is not a terminal, which is the answer that makes test output and
// piped output reproducible.
func TestIsTerminal_is_false_for_anything_that_is_not_a_file(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("a buffer must not be treated as a terminal")
	}
}

// https://no-color.org: any value, including empty-but-set, disables colour.
func TestIsTerminal_honours_NO_COLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if isTerminal(os.Stdout) {
		t.Error("NO_COLOR set to empty must still disable styling")
	}
	t.Setenv("NO_COLOR", "1")
	if isTerminal(os.Stdout) {
		t.Error("NO_COLOR=1 must disable styling")
	}
}

// Emoji mean nothing in a CI log or a grep, and a lone replacement character
// is worse than a word.
func TestStatusLines_are_plain_when_not_a_terminal(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw)

	p.Ok("started")
	p.Warn("slow")

	if strings.Contains(out.String(), "✅") || strings.Contains(errw.String(), "⚠") {
		t.Errorf("emoji written to a non-terminal:\nout=%q\nerr=%q", out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "OK started") {
		t.Errorf("out = %q, want the plain marker", out.String())
	}
	if !strings.Contains(errw.String(), "WARN slow") {
		t.Errorf("err = %q, want the plain marker", errw.String())
	}
}

// Warnings must not contaminate a stdout that is being piped.
func TestWarnAndFail_go_to_stderr(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw)

	p.Warn("careful")
	p.Fail("broken")

	if out.Len() != 0 {
		t.Errorf("stdout got %q; warnings and failures belong on stderr", out.String())
	}
	if !strings.Contains(errw.String(), "careful") || !strings.Contains(errw.String(), "broken") {
		t.Errorf("stderr = %q", errw.String())
	}
}

// In JSON mode the caller writes one document. A stray status line would make
// it unparseable.
func TestJSONMode_suppresses_status_lines(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw).WithJSON(true)

	p.Ok("started")
	p.Info("detail")
	p.Printf("raw\n")
	p.Warn("careful")

	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing but the JSON document", out.String())
	}
	if errw.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errw.String())
	}
}

func TestTable_aligns_for_a_person(t *testing.T) {
	var out bytes.Buffer
	p := New(&out, &out)

	if err := p.Table([]string{"HOST", "ROLE"}, [][]string{{"10.0.0.1", "node"}, {"10.0.0.2", "ns"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	body := out.String()
	for _, want := range []string{"HOST", "ROLE", "10.0.0.1", "10.0.0.2"} {
		if !strings.Contains(body, want) {
			t.Errorf("table is missing %q:\n%s", want, body)
		}
	}
}

// One call site serves both audiences, so a table and its JSON cannot drift.
func TestTable_in_JSON_mode_emits_objects_keyed_by_header(t *testing.T) {
	var out bytes.Buffer
	p := New(&out, &out).WithJSON(true)

	if err := p.Table([]string{"HOST", "NODE ID"}, [][]string{{"10.0.0.1", "peerA"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}

	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0]["host"] != "10.0.0.1" {
		t.Errorf("host = %q", rows[0]["host"])
	}
	if rows[0]["node_id"] != "peerA" {
		t.Errorf("node_id = %q; a header with a space must become one key", rows[0]["node_id"])
	}
}

// A short row must not panic or shift the remaining columns.
func TestTable_in_JSON_mode_tolerates_a_short_row(t *testing.T) {
	var out bytes.Buffer
	p := New(&out, &out).WithJSON(true)

	if err := p.Table([]string{"A", "B", "C"}, [][]string{{"1", "2"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if rows[0]["A"] != "" && rows[0]["a"] != "1" {
		t.Errorf("row = %v", rows[0])
	}
	if _, present := rows[0]["c"]; present {
		t.Errorf("a column the row does not have must be absent, got %v", rows[0])
	}
}

func TestWithJSON_does_not_mutate_the_original(t *testing.T) {
	var out bytes.Buffer
	p := New(&out, &out)
	_ = p.WithJSON(true)

	if p.JSONMode() {
		t.Error("WithJSON must return a copy, not change the receiver")
	}
}
