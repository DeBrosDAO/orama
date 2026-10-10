package installers

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// torrcDirectives parses a torrc into directive -> values, ignoring comments.
func torrcDirectives(torrc string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(torrc, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		out[key] = append(out[key], value)
	}
	return out
}

func TestGenerateTorrc_isClientOnly(t *testing.T) {
	d := torrcDirectives(GenerateTorrc())
	want := map[string]string{
		"ClientOnly":                    "1",
		"ORPort":                        "0",
		"DirPort":                       "0",
		"ExitRelay":                     "0",
		"ClientRejectInternalAddresses": "1",
	}
	for key, value := range want {
		if got := d[key]; len(got) != 1 || got[0] != value {
			t.Errorf("%s = %v, want exactly %q", key, got, value)
		}
	}
	// No relay, bridge or control surface of any kind.
	for _, forbidden := range []string{"ControlPort", "ControlSocket", "BridgeRelay", "ExitPolicy", "Nickname", "ContactInfo", "MyFamily", "HiddenServiceDir"} {
		if _, ok := d[forbidden]; ok {
			t.Errorf("client-only torrc must not set %s", forbidden)
		}
	}
}

func TestGenerateTorrc_socksPortIsTheSharedConstantWithIsolation(t *testing.T) {
	d := torrcDirectives(GenerateTorrc())
	ports := d["SocksPort"]
	if len(ports) != 1 {
		t.Fatalf("SocksPort lines = %v, want exactly one", ports)
	}
	fields := strings.Fields(ports[0])
	if fields[0] != constants.TorSOCKSAddr() {
		t.Errorf("SocksPort address = %q, want %q (what the gateway dials)", fields[0], constants.TorSOCKSAddr())
	}
	if !strings.HasPrefix(fields[0], "127.0.0.1:") {
		t.Errorf("SocksPort %q must bind loopback only", fields[0])
	}
	// The tunnel's per-user circuits depend on stream isolation by SOCKS auth.
	if len(fields) < 2 || fields[1] != "IsolateSOCKSAuth" {
		t.Errorf("SocksPort flags = %v, want IsolateSOCKSAuth", fields[1:])
	}
}

func TestGenerateTorrc_dataDirAndJournalLogging(t *testing.T) {
	d := torrcDirectives(GenerateTorrc())
	if got := d["DataDirectory"]; len(got) != 1 || got[0] != TorDataDir {
		t.Errorf("DataDirectory = %v, want %q", got, TorDataDir)
	}
	if got := d["Log"]; len(got) != 1 || got[0] != "notice stdout" {
		t.Errorf("Log = %v, want notice stdout (journald via the unit)", got)
	}
}

func TestTorSuiteFor(t *testing.T) {
	for codename, want := range map[string]string{"noble": "noble", "jammy": "jammy", "bookworm": "bookworm"} {
		got, err := TorSuiteFor(codename)
		if err != nil || got != want {
			t.Errorf("TorSuiteFor(%q) = %q, %v; want %q", codename, got, err, want)
		}
	}
}

// deb.torproject.org has no suite for these; pointing apt at one would fail
// later with a far less useful message.
func TestTorSuiteFor_unpublishedCodenameIsAnError(t *testing.T) {
	for _, codename := range []string{"plucky", "", "focal"} {
		if _, err := TorSuiteFor(codename); err == nil {
			t.Errorf("TorSuiteFor(%q) must fail", codename)
		}
	}
}

func TestTorAptSource_isSignedByThePinnedKeyring(t *testing.T) {
	src := TorAptSource("noble")
	for _, want := range []string{
		"URIs: https://deb.torproject.org/torproject.org/",
		"Suites: noble",
		"Components: main",
		"Signed-By: " + TorKeyringPath,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("apt source missing %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "http://") {
		t.Error("the Tor repository must be fetched over https")
	}
}

func TestTorArchiveKeyFingerprint_isAFullV4Fingerprint(t *testing.T) {
	if !regexp.MustCompile(`^[0-9A-F]{40}$`).MatchString(TorArchiveKeyFingerprint) {
		t.Errorf("TorArchiveKeyFingerprint %q is not a 40-hex fingerprint", TorArchiveKeyFingerprint)
	}
}

const torKeyColons = "pub:-:4096:1:EE8CBC9E886DDD89:1252006614:1756252800::-:::scSC::::::23::0:\n" +
	"fpr:::::::::A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89:\n" +
	"uid:-::::1252006614::ABCDEF::deb.torproject.org archive signing key::::::::::0:\n" +
	"sub:-:2048:1:74A941BA219EC810:1331650547:1756252800:::::s::::::23:\n" +
	"fpr:::::::::2265EB4CB2BF88D900AE8D1B74A941BA219EC810:\n"

func TestPrimaryKeyFingerprints_skipsSubkeys(t *testing.T) {
	got := PrimaryKeyFingerprints(torKeyColons)
	if len(got) != 1 || got[0] != "A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89" {
		t.Errorf("PrimaryKeyFingerprints = %v, want only the pub key's fingerprint", got)
	}
}

func TestPrimaryKeyFingerprints_noKey(t *testing.T) {
	for _, in := range []string{"", "garbage", "fpr:::::::::AAAA:"} {
		if got := PrimaryKeyFingerprints(in); len(got) != 0 {
			t.Errorf("PrimaryKeyFingerprints(%q) = %v, want none", in, got)
		}
	}
}

func TestVerifyTorArchiveKey_acceptsThePinnedKey(t *testing.T) {
	if err := VerifyTorArchiveKey(torKeyColons); err != nil {
		t.Errorf("the pinned key must verify: %v", err)
	}
}

func TestVerifyTorArchiveKey_rejectsAnotherKey(t *testing.T) {
	other := strings.ReplaceAll(torKeyColons, TorArchiveKeyFingerprint, "0000000000000000000000000000000000000000")
	if err := VerifyTorArchiveKey(other); err == nil {
		t.Error("a key with another fingerprint must be refused")
	}
	if err := VerifyTorArchiveKey(""); err == nil {
		t.Error("gpg output with no key must be refused")
	}
}

// Every key in the file ends up in the keyring apt trusts, so a genuine key
// bundled with a second one is as bad as a wrong key.
func TestVerifyTorArchiveKey_rejectsAnExtraKey(t *testing.T) {
	bundled := torKeyColons +
		"pub:-:4096:1:1111111111111111:1252006614:::-:::scSC::::::23::0:\n" +
		"fpr:::::::::1111111111111111111111111111111111111111:\n"
	if err := VerifyTorArchiveKey(bundled); err == nil {
		t.Error("a key file carrying a second primary key must be refused")
	}
}

func TestParseOSReleaseCodename(t *testing.T) {
	osRelease := "NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\n"
	if got := ParseOSReleaseCodename(osRelease); got != "noble" {
		t.Errorf("codename = %q, want noble", got)
	}
	if got := ParseOSReleaseCodename("VERSION_CODENAME=\"bookworm\"\n"); got != "bookworm" {
		t.Errorf("quoted codename = %q, want bookworm", got)
	}
	if got := ParseOSReleaseCodename("ID=debian\n"); got != "" {
		t.Errorf("missing codename = %q, want empty", got)
	}
}

// The distro units bind 9050 with /etc/tor/torrc; both must be masked.
func TestTorDistroUnits_coverTheDefaultInstance(t *testing.T) {
	want := map[string]bool{"tor.service": false, "tor@default.service": false}
	for _, u := range TorDistroUnits {
		want[u] = true
	}
	for u, seen := range want {
		if !seen {
			t.Errorf("TorDistroUnits must include %s", u)
		}
	}
}
