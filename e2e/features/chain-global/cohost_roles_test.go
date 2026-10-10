//go:build e2e_fleet

package chainglobal

import (
	"strings"
	"testing"
)

func TestRoleProblems_perPreferencesAndInstall(t *testing.T) {
	cases := map[string]struct {
		prefs     string
		colocated bool
		want      string
	}{
		"fleet node, no role line":          {"branch: main\nnameserver: true\n", false, ""},
		"fleet node, explicit cluster role": {"role: cluster\n", false, ""},
		"empty preferences":                 {"", false, ""},
		"fleet node turned global":          {"role: global\n", false, `records role "global"`},
		"fleet node turned both":            {"role: both\nglobal_netns: orama-global\n", false, `records role "both"`},
		"stagenet node, both and netns":     {"branch: main\nrole: both\nglobal_netns: orama-global\n", true, ""},
		"stagenet node, no role":            {"branch: main\n", true, `records role ""`},
		"stagenet node, global":             {"role: global\nglobal_netns: orama-global\n", true, `records role "global"`},
		"stagenet node, no netns":           {"role: both\n", true, "records global_netns"},
		"stagenet node, other netns":        {"role: both\nglobal_netns: other\n", true, `records global_netns "other"`},
	}
	for name, tc := range cases {
		got := strings.Join(roleProblems(tc.prefs, tc.colocated), "; ")
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems %q, want %q", name, got, tc.want)
		}
	}
}
