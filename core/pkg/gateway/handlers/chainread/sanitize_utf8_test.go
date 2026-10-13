package chainread

import (
	"strings"
	"testing"
)

// The patterns are matched on the log as the node wrote it, and bytes that are not valid UTF-8 are
// dropped afterwards. Dropping them after the match glued the pieces of an address or a path back
// together, past the patterns. Found by FuzzGatewaySanitizeLog.
func TestSanitizeLog_aCutDoesNotLeaveTrailingSpace(t *testing.T) {
	log := "reason" + strings.Repeat(" ", 2*logMaxRunes) + "tail"
	if out := sanitizeLog(log); out != "reason" {
		t.Fatalf("got %d runes ending in %q, want the reason alone", len(out), out[len(out)-1:])
	}
}

func TestSanitizeLog_invalidUTF8CannotHideWhatIsRedacted(t *testing.T) {
	for name, log := range map[string]string{
		"ipv4":     "dial 10.0.0.\xff1 refused",
		"ipv4 mid": "dial 10.0\xff.0.1:26657 refused",
		"path":     "open /va\xffr/lib/orama/x.db",
		"location": "at baseapp\xff.go:1066",
		"control":  "dial 10.0.0.\x001 refused",
	} {
		out := sanitizeLog(log)
		for _, leak := range []string{"10.0.0.1", "/var", "baseapp.go"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: %q leaks %q", name, out, leak)
			}
		}
	}
}
