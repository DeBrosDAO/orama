package roomid

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"simple", "standup", true},
		{"uuid and punctuation", "call:3f2b-9c.A_b@x/y+z=", true},
		{"max length", strings.Repeat("a", MaxLen), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", MaxLen+1), false},
		{"space", "my room", false},
		{"newline", "a\nb", false},
		{"nul", "a\x00b", false},
		{"non-ascii", "salón", false},
		{"delete", "a\x7fb", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.id); (err == nil) != c.ok {
				t.Fatalf("Validate(%q) = %v, want ok=%v", c.id, err, c.ok)
			}
		})
	}
}
