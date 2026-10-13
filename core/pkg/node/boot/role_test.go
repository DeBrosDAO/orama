package boot

import (
	"errors"
	"strings"
	"testing"
)

func layoutOK() error { return nil }

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
		{raw: "both", want: RoleBoth},
		{raw: " BOTH ", want: RoleBoth},
		{raw: "worker", bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseRole(tc.raw, layoutOK)
			if tc.bad {
				if err == nil {
					t.Fatalf("ParseRole(%q) = %q, want an error", tc.raw, got)
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

func TestParseRole_bothNeedsTheNetnsLayout(t *testing.T) {
	problem := errors.New("orama-global-netns.service is missing")
	_, err := ParseRole("both", func() error { return problem })
	if err == nil || !errors.Is(err, problem) || !strings.Contains(err.Error(), "--colocated") {
		t.Fatalf("err = %v, want the layout problem and what to run", err)
	}
	if _, err := ParseRole("both", nil); err == nil {
		t.Fatal("both was accepted with no layout check")
	}
}

func TestParseRole_otherRolesNeverConsultTheLayout(t *testing.T) {
	boom := func() error { return errors.New("must not be called") }
	for _, raw := range []string{"", "cluster", "global"} {
		if _, err := ParseRole(raw, boom); err != nil {
			t.Errorf("ParseRole(%q) consulted the layout: %v", raw, err)
		}
	}
}

func TestRole_RunsClusterGraph(t *testing.T) {
	for role, want := range map[Role]bool{RoleCluster: true, RoleBoth: true, RoleGlobal: false} {
		if role.RunsClusterGraph() != want {
			t.Errorf("%s.RunsClusterGraph() = %v, want %v", role, !want, want)
		}
	}
}
