package cli

import "testing"

func TestSessionPolicyFields(t *testing.T) {
	for name, tc := range map[string]struct{ signIn, device, want string }{
		"neither reads":     {"", "", ""},
		"sign-in only":      {"open", "", `{"sign_in":"open"}`},
		"device only":       {"", "required", `{"device_policy":"required"}`},
		"both":              {"members", "approval", `{"device_policy":"approval","sign_in":"members"}`},
		"unvalidated value": {"everyone", "", `{"sign_in":"everyone"}`},
	} {
		got, err := sessionPolicyFields(tc.signIn, tc.device)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, tc.want)
		}
		if tc.want == "" && got != nil {
			t.Errorf("%s: a read carried a body %q", name, got)
		}
	}
}
