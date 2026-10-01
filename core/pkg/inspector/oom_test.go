package inspector

import "testing"

func TestParseOOMKillsField(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantN   int
		wantErr bool
	}{
		{"zero", "0\n", 0, false},
		{"some", " 3 ", 3, false},
		{"unknown", "unknown", 0, true},
		{"empty", "", 0, true},
		{"negative", "-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, errMsg := parseOOMKillsField(tt.in)
			if n != tt.wantN || (errMsg != "") != tt.wantErr {
				t.Fatalf("parseOOMKillsField(%q) = (%d, %q)", tt.in, n, errMsg)
			}
		})
	}
}
