package auth

import (
	"testing"
	"time"
)

// A column comes back as a different Go type depending on who ran the query,
// and the three answers parseTimestamp gives are acted on separately: a NULL
// means nobody wrote a value, a readable value is one, and a value nobody can
// read is a value somebody wrote wrong. Callers fail closed on the third, which
// only works if it is told apart from the first two.
func TestParseTimestamp(t *testing.T) {
	moment := time.Date(2026, 9, 5, 8, 30, 0, 0, time.UTC)

	for _, tc := range []struct {
		name     string
		cell     any
		want     time.Time
		present  bool
		readable bool
	}{
		{name: "NULL", cell: nil, present: false, readable: true},
		{name: "an empty string is the same as NULL", cell: "", present: false, readable: true},
		{name: "whitespace is the same as NULL", cell: "   ", present: false, readable: true},
		{name: "the driver's own time.Time", cell: moment, want: moment, present: true, readable: true},
		{name: "the SQLite text form rqlite returns", cell: "2026-09-05 08:30:00", want: moment, present: true, readable: true},
		{name: "RFC3339", cell: "2026-09-05T08:30:00Z", want: moment, present: true, readable: true},
		{name: "bytes, which is what a raw driver hands back", cell: []byte("2026-09-05 08:30:00"), want: moment, present: true, readable: true},
		{name: "a value nobody can read", cell: "whenever", present: true, readable: false},
		{name: "a number is not a timestamp", cell: 17, present: true, readable: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, present, readable := parseTimestamp(tc.cell)
			if present != tc.present || readable != tc.readable {
				t.Fatalf("present=%v readable=%v, want present=%v readable=%v",
					present, readable, tc.present, tc.readable)
			}
			if !at.Equal(tc.want) {
				t.Errorf("at = %v, want %v", at, tc.want)
			}
		})
	}
}

// A key's fingerprint has to be a hash whatever the cluster is configured with.
// HashAPIKey returns the key unchanged when no HMAC secret is set, which is why
// it is not what goes into a response.
func TestKeyFingerprint_isAlwaysAHash(t *testing.T) {
	const key = "orama_sk_payload_check"

	fp := KeyFingerprint(key)
	if fp == "" || fp == key {
		t.Fatalf("KeyFingerprint(%q) = %q", key, fp)
	}
	if fp != KeyFingerprint(key) {
		t.Error("the same key fingerprinted differently twice")
	}
	if KeyFingerprint("orama_sk_other_check") == fp {
		t.Error("two keys share a fingerprint")
	}
	if KeyFingerprint("") != "" {
		t.Error("no key fingerprints as something")
	}
}
