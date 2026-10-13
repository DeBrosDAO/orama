package auth

import (
	"strings"
	"testing"
	"time"
)

// A verified workload token whose subject names one namespace and whose claim
// names another is refused at verification, not only when it asks to renew.
func TestParseAndVerifyJWT_refusesAWorkloadSubjectInAnotherNamespace(t *testing.T) {
	s, _ := serviceWithRevocations(t)

	for name, tc := range map[string]struct {
		ns, subject string
		wantErr     string
	}{
		"subject names another namespace": {"acme", WorkloadSubject("other", "web"), "other"},
		"subject names no deployment":     {"acme", WorkloadSubjectPrefix + "acme", "does not name a deployment"},
		"subject matches, case differs":   {"acme", WorkloadSubject("ACME", "web"), ""},
		"subject matches":                 {"acme", WorkloadSubject("acme", "web"), ""},
		"a wallet is not a workload":      {"acme", "0xabc", ""},
	} {
		t.Run(name, func(t *testing.T) {
			token, _, err := s.GenerateJWT(tc.ns, tc.subject, time.Hour, nil)
			if err != nil {
				t.Fatalf("GenerateJWT: %v", err)
			}
			_, err = s.ParseAndVerifyJWT(token)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("a consistent token was refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}
