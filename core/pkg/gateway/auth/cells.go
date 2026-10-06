package auth

import (
	"fmt"
	"strings"
	"time"
)

// Reading a row back.
//
// The same query returns different Go types depending on who ran it: the rqlite
// client hands back strings for everything, and go-sqlite3 — which is what the
// tests run against — parses a TIMESTAMP column into a time.Time and a numeric
// one into an int64. Code that reads a row has to accept both, or it passes its
// tests and misreads production.

// getStringVal reads a column as text.
func getStringVal(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// timestampLayouts are the shapes a TIMESTAMP column comes back in.
var timestampLayouts = []string{
	sqliteTime,
	time.RFC3339,
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05 -0700 MST",
}

// parseTimestamp reads a TIMESTAMP column, and says whether it could.
//
// The three answers are genuinely different and callers act on all three: NULL
// (no value was written), a value, and a value nobody can read. Collapsing the
// third into either of the others is how a column that says "retired" or
// "expired" ends up meaning "live".
func parseTimestamp(cell any) (at time.Time, present, readable bool) {
	switch value := cell.(type) {
	case nil:
		return time.Time{}, false, true
	case time.Time:
		return value.UTC(), true, true
	}

	text := strings.TrimSpace(getStringVal(cell))
	if text == "" {
		return time.Time{}, false, true
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC(), true, true
		}
	}
	return time.Time{}, true, false
}
