package functions

import (
	"testing"
)

// ----------------------------------------------------------------------------
// stringField — pulls value from JSON-decoded map under any of the given keys
// ----------------------------------------------------------------------------

func TestStringField_prefersFirstKey(t *testing.T) {
	m := map[string]interface{}{
		"id": "first",
		"ID": "second",
	}
	if got := stringField(m, "id", "ID"); got != "first" {
		t.Errorf("stringField = %q, want %q", got, "first")
	}
}

func TestStringField_fallsThroughWhenFirstMissing(t *testing.T) {
	m := map[string]interface{}{
		"ID": "second",
	}
	if got := stringField(m, "id", "ID"); got != "second" {
		t.Errorf("stringField = %q, want %q", got, "second")
	}
}

func TestStringField_emptyValueSkipped(t *testing.T) {
	// An empty string under the first key MUST fall through to subsequent
	// keys, otherwise empty pubsub `topic` fields would shadow valid
	// PascalCase `Topic`.
	m := map[string]interface{}{
		"id": "",
		"ID": "fallback",
	}
	if got := stringField(m, "id", "ID"); got != "fallback" {
		t.Errorf("stringField = %q, want %q", got, "fallback")
	}
}

func TestStringField_nonStringValueSkipped(t *testing.T) {
	m := map[string]interface{}{
		"id": 42,
		"ID": "ok",
	}
	if got := stringField(m, "id", "ID"); got != "ok" {
		t.Errorf("stringField = %q, want %q", got, "ok")
	}
}

func TestStringField_allMissingReturnsEmpty(t *testing.T) {
	m := map[string]interface{}{"other": "value"}
	if got := stringField(m, "id", "ID"); got != "" {
		t.Errorf("stringField = %q, want empty", got)
	}
}

// ----------------------------------------------------------------------------
// formatCronTimestamp — RFC3339 -> UTC display, "-" for missing/unparseable
// ----------------------------------------------------------------------------

func TestFormatCronTimestamp_nilReturnsDash(t *testing.T) {
	if got := formatCronTimestamp(nil); got != "-" {
		t.Errorf("formatCronTimestamp(nil) = %q, want %q", got, "-")
	}
}

func TestFormatCronTimestamp_emptyStringReturnsDash(t *testing.T) {
	if got := formatCronTimestamp(""); got != "-" {
		t.Errorf("formatCronTimestamp(\"\") = %q, want %q", got, "-")
	}
}

func TestFormatCronTimestamp_nonStringReturnsDash(t *testing.T) {
	if got := formatCronTimestamp(42); got != "-" {
		t.Errorf("formatCronTimestamp(42) = %q, want %q", got, "-")
	}
}

func TestFormatCronTimestamp_rfc3339(t *testing.T) {
	got := formatCronTimestamp("2025-05-08T03:00:00Z")
	want := "2025-05-08 03:00:00 UTC"
	if got != want {
		t.Errorf("formatCronTimestamp = %q, want %q", got, want)
	}
}

func TestFormatCronTimestamp_rfc3339Nano(t *testing.T) {
	got := formatCronTimestamp("2025-05-08T03:00:00.123456789Z")
	want := "2025-05-08 03:00:00 UTC"
	if got != want {
		t.Errorf("formatCronTimestamp nano = %q, want %q", got, want)
	}
}

func TestFormatCronTimestamp_rfc3339WithOffset(t *testing.T) {
	// Non-UTC offsets must be normalised to UTC for the display.
	got := formatCronTimestamp("2025-05-08T05:00:00+02:00")
	want := "2025-05-08 03:00:00 UTC"
	if got != want {
		t.Errorf("formatCronTimestamp offset = %q, want %q", got, want)
	}
}

func TestFormatCronTimestamp_unparseableFallsBackToRaw(t *testing.T) {
	// If the server returns an unexpected timestamp shape, surface it
	// rather than silently dropping to "-" — operator visibility wins.
	got := formatCronTimestamp("not-a-timestamp")
	if got != "not-a-timestamp" {
		t.Errorf("formatCronTimestamp unparseable = %q, want raw passthrough", got)
	}
}
