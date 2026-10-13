package db

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintQueryResult_rowsAreArraysInColumnOrder(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"columns":["id","name"],"rows":[[1,"alpha"],[2,"beta"]]}`)
	if err := printQueryResult(&buf, body); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"id", "name", "alpha", "beta", "Rows returned: 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
}

func TestPrintQueryResult_writeReportsTheInsertID(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"rows_affected":1,"last_insert_id":4}`)
	if err := printQueryResult(&buf, body); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "Rows affected: 1") || !strings.Contains(got, "Last insert id: 4") {
		t.Fatalf("got %s", got)
	}
}
