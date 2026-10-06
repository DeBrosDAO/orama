package traffic

import (
	"strings"
	"testing"
)

func TestSanitizeLabel_cases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "anchat", "anchat"},
		{"mixed case digits dash underscore", "Anchat-v2_test9", "Anchat-v2_test9"},
		{"index", "index", "index"},
		{"empty", "", InvalidLabel},
		{"leading dash", "-evil", InvalidLabel},
		{"leading underscore", "_evil", InvalidLabel},
		{"dot", "a.b", InvalidLabel},
		{"slash", "a/b", InvalidLabel},
		{"space", "a b", InvalidLabel},
		{"unicode", "ανχατ", InvalidLabel},
		{"reserved other cannot be forged", OtherLabel, InvalidLabel},
		{"at max length", strings.Repeat("a", MaxLabelLen), strings.Repeat("a", MaxLabelLen)},
		{"over max length", strings.Repeat("a", MaxLabelLen+1), InvalidLabel},
	}
	for _, tc := range cases {
		if got := sanitizeLabel(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeLabel(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
