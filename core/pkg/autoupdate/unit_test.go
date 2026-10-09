package autoupdate

import (
	"os"
	"strings"
	"testing"
)

// The unit runs as root and swaps the release under /opt/orama, so what the
// kernel and the account can be made to do is confined. What stays open is what
// `orama node upgrade --restart` does as part of the install.
func TestUnit_isConfinedAndStillCanDoWhatTheUpgradeDoes(t *testing.T) {
	unit, err := os.ReadFile("../../systemd/orama-autoupdate.service")
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, line := range strings.Split(string(unit), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			settings[key] = value
		}
	}
	for key, want := range map[string]string{
		"NoNewPrivileges":         "yes",
		"PrivateTmp":              "yes",
		"ProtectHome":             "yes",
		"ProtectControlGroups":    "yes",
		"LockPersonality":         "yes",
		"RestrictSUIDSGID":        "yes",
		"RestrictAddressFamilies": "AF_INET AF_INET6 AF_UNIX AF_NETLINK",
	} {
		if got := settings[key]; got != want {
			t.Errorf("%s=%q, want %q", key, got, want)
		}
	}
	// The upgrade writes sysctls (pkg/install/firewall.go) and the system's own
	// files; these would stop it.
	for _, key := range []string{"ProtectKernelTunables", "ProtectSystem"} {
		if v, ok := settings[key]; ok {
			t.Errorf("%s=%s is set: `orama node upgrade --restart` (sysctl -w, writes under /usr and /etc) cannot run under it", key, v)
		}
	}
}
