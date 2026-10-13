package main

import "testing"

// starttime is read after the last ')', so a command name holding spaces and
// parentheses cannot shift the fields.
func TestParseStartTime(t *testing.T) {
	const tail = " S 1 1234 1234 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 12345678 900"
	for _, comm := range []string{"(orama-node)", "(a b) (c))"} {
		got, err := parseStartTime("4321 " + comm + tail)
		if err != nil || got != 987654 {
			t.Errorf("%s: got %d, %v; want 987654", comm, got, err)
		}
	}
	for _, bad := range []string{"", "4321 orama-node S 1", "4321 (x) S 1 2 3", "4321 (x)" + " S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 notanumber"} {
		if _, err := parseStartTime(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
