package boot

import (
	"strings"
	"testing"
)

func TestParseRole(t *testing.T) {
	tests := []struct {
		raw  string
		want Role
		bad  bool
	}{
		{raw: "", want: RoleCluster},
		{raw: "cluster", want: RoleCluster},
		{raw: " CLUSTER ", want: RoleCluster},
		{raw: "global", want: RoleGlobal},
		{raw: "Global", want: RoleGlobal},
		{raw: "both", bad: true},
		{raw: "BOTH", bad: true},
		{raw: "worker", bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseRole(tc.raw)
			if tc.bad {
				if err == nil {
					t.Fatalf("ParseRole(%q) = %q, want an error", tc.raw, got)
				}
				if strings.EqualFold(strings.TrimSpace(tc.raw), "both") && !strings.Contains(err.Error(), "both") {
					t.Fatalf("ParseRole(%q) error %v does not say what to do about both", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRole(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseRole(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
