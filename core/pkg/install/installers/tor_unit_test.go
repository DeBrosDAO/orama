package installers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// torUnitDirectives reads core/systemd/orama-namespace-tor@.service into
// directive -> values.
func torUnitDirectives(t *testing.T) map[string][]string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "systemd", "orama-namespace-tor@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		out[key] = append(out[key], value)
	}
	return out
}

// The unit and the installer must agree on the torrc and the DataDirectory.
func TestTorUnit_runsTheInstallerTorrcAndDataDir(t *testing.T) {
	d := torUnitDirectives(t)
	if got := d["ExecStart"]; len(got) != 1 || got[0] != "/usr/bin/tor -f "+constants.TorConfigPath {
		t.Errorf("ExecStart = %v, want tor -f %s", got, constants.TorConfigPath)
	}
	if got := d["StateDirectory"]; len(got) != 1 || "/var/lib/"+got[0] != TorDataDir {
		t.Errorf("StateDirectory = %v, want the directory of TorDataDir %s", got, TorDataDir)
	}
	if got := d["StateDirectoryMode"]; len(got) != 1 || got[0] != "0700" {
		t.Errorf("StateDirectoryMode = %v, want 0700 (Tor refuses a group-readable DataDirectory)", got)
	}
}

// bugboard #244: the Anyone client was the least hardened unit on the node.
// The Tor client must not repeat that.
func TestTorUnit_isHardened(t *testing.T) {
	d := torUnitDirectives(t)
	want := map[string]string{
		"User":                    "debian-tor",
		"Group":                   "debian-tor",
		"NoNewPrivileges":         "yes",
		"CapabilityBoundingSet":   "",
		"ProtectSystem":           "strict",
		"ProtectHome":             "yes",
		"PrivateTmp":              "yes",
		"PrivateDevices":          "yes",
		"ProtectKernelTunables":   "yes",
		"ProtectKernelModules":    "yes",
		"ProtectControlGroups":    "yes",
		"ProtectProc":             "invisible",
		"RestrictNamespaces":      "yes",
		"RestrictSUIDSGID":        "yes",
		"MemoryDenyWriteExecute":  "yes",
		"SystemCallFilter":        "@system-service",
		"RestrictAddressFamilies": "AF_UNIX AF_INET AF_INET6",
		"IPAddressAllow":          "localhost",
		"UMask":                   "0077",
	}
	for key, value := range want {
		if got := d[key]; len(got) != 1 || got[0] != value {
			t.Errorf("%s = %v, want %q", key, got, value)
		}
	}
	for _, key := range []string{"ReadWritePaths", "AmbientCapabilities"} {
		for _, v := range d[key] {
			if v != "" {
				t.Errorf("%s = %q; the Tor client needs no extra write paths or capabilities", key, v)
			}
		}
	}
}

// Tor must not be able to open a connection into the WireGuard overlay or any
// other internal range.
func TestTorUnit_deniesInternalNetworks(t *testing.T) {
	deny := strings.Join(torUnitDirectives(t)["IPAddressDeny"], " ")
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fc00::/7"} {
		if !strings.Contains(deny, cidr) {
			t.Errorf("IPAddressDeny %q is missing %s", deny, cidr)
		}
	}
}

func TestTorUnit_isSupervisedLikeTheOtherIndexUnits(t *testing.T) {
	d := torUnitDirectives(t)
	for key, value := range map[string]string{
		"PartOf":                "orama-node.service",
		"StartLimitIntervalSec": "0",
		"Restart":               "always",
		"SyslogIdentifier":      "orama-tor-%i",
	} {
		if got := d[key]; len(got) != 1 || got[0] != value {
			t.Errorf("%s = %v, want %q", key, got, value)
		}
	}
	// Tor runs as debian-tor. An env file would be written by the orama user
	// for a process it does not own, so the unit reads none and
	// orama-privhelper refuses to write one.
	if got := d["EnvironmentFile"]; len(got) != 0 {
		t.Errorf("EnvironmentFile = %v, want none", got)
	}
}
