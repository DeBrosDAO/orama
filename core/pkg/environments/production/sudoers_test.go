package production

import (
	"strings"
	"testing"
)

// Regression test for the bug where the orama user could not run `ufw`, so
// AddWebRTCRules silently failed and TURN relay ports stayed firewalled after
// `webrtc enable`. The sudoers rule MUST grant the ufw commands the runtime
// invokes, using the resolved absolute ufw path (sudoers matches by path).
func TestOramaSudoersRule_grantsUfwAndSystemctl(t *testing.T) {
	rule := oramaSudoersRule("/bin/systemctl", "/usr/sbin/ufw")

	for _, want := range []string{
		"orama ALL=(root) NOPASSWD:",
		// ufw — the fix
		"/usr/sbin/ufw allow *",
		"/usr/sbin/ufw delete allow *",
		"/usr/sbin/ufw reload",
		"/usr/sbin/ufw status",
		"/usr/sbin/ufw status verbose",
		// systemctl — pre-existing grants must be preserved
		"/bin/systemctl daemon-reload",
		"/bin/systemctl restart orama-namespace-*",
		"/bin/systemctl start orama-deploy-*",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("sudoers rule missing %q\nrule: %s", want, rule)
		}
	}

	// A sudoers drop-in rule must be a single logical line.
	if got := strings.Count(strings.TrimRight(rule, "\n"), "\n"); got != 0 {
		t.Errorf("sudoers rule must be one line, found %d embedded newlines:\n%s", got, rule)
	}
}

// The absolute ufw path must appear verbatim — sudoers matches commands by
// absolute path, so a bare "ufw" (or the wrong path) would never match.
func TestOramaSudoersRule_usesResolvedPaths(t *testing.T) {
	rule := oramaSudoersRule("/usr/bin/systemctl", "/sbin/ufw")
	if !strings.Contains(rule, "/sbin/ufw allow *") {
		t.Errorf("rule did not use the resolved ufw path: %s", rule)
	}
	if strings.Contains(rule, " ufw allow") { // bare "ufw" would not match in sudoers
		t.Errorf("rule contains a bare (non-absolute) ufw reference: %s", rule)
	}
}
