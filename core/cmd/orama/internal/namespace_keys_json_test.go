package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// created is what the gateway answers to POST /v1/namespace/keys.
func created() map[string]any {
	return map[string]any{
		"id": float64(7), "scopes": "invoke,storage", "namespace": "acme",
		"expires_at": "2027-01-01T00:00:00Z", "label": "ci", "api_key": "ak_secret",
	}
}

func TestPrintCreatedKey_jsonIsOneObjectWithTheKey(t *testing.T) {
	var out, errw bytes.Buffer
	if err := printCreatedKey(printer.New(&out, &errw).WithJSON(true), created()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("--json printed something that is not JSON: %v\n%s", err, out.String())
	}
	want := map[string]any{
		"id": float64(7), "scopes": "invoke,storage", "namespace": "acme",
		"expires": "2027-01-01T00:00:00Z", "label": "ci", "key": "ak_secret",
	}
	if len(got) != len(want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestPrintCreatedKey_jsonWithoutALabelHasAnEmptyOne(t *testing.T) {
	result := created()
	delete(result, "label")
	var out bytes.Buffer
	if err := printCreatedKey(printer.New(&out, &out).WithJSON(true), result); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if label, ok := got["label"]; !ok || label != "" {
		t.Errorf("label = %v (present %v), want an empty string", label, ok)
	}
}

func TestPrintCreatedKey_textKeepsTheLinesAPersonReads(t *testing.T) {
	var out bytes.Buffer
	if err := printCreatedKey(printer.New(&out, &out), created()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"API key created.", "id:        7", "scopes:    invoke,storage", "namespace: acme", "label:     ci", "API KEY (shown once", "ak_secret"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if json.Valid(out.Bytes()) {
		t.Error("the text output without --json is JSON")
	}
}
