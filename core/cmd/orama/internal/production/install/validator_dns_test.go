package install

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A joining nameserver was told its domain resolves to the wrong address — the
// nodes already serving it — and that certificates "may fail", which is true
// of neither DNS-01 issuance nor that node.
func TestValidateDNS_nameserverDoesNotCheckTheARecord(t *testing.T) {
	v := NewValidator(&Flags{Domain: "cluster.invalid", VpsIP: "203.0.113.7", Nameserver: true}, t.TempDir())
	out := captureStdout(t, v.ValidateDNS)
	if strings.Contains(out, "mismatch") || strings.Contains(out, "lookup failed") {
		t.Errorf("a nameserver install checked the A record: %q", out)
	}
	if !strings.Contains(out, "DNS-01") || !strings.Contains(out, "delegate") {
		t.Errorf("the note does not say what DNS-01 needs: %q", out)
	}
}

func TestValidateDNS_noDomainSaysNothing(t *testing.T) {
	v := NewValidator(&Flags{VpsIP: "203.0.113.7"}, t.TempDir())
	if out := captureStdout(t, v.ValidateDNS); out != "" {
		t.Errorf("printed %q with no domain to validate", out)
	}
}

// --vps-ip becomes node.public_ip; invites and upgrades refuse anything but a
// public IPv4 address, so install refuses it too rather than recording it.
func TestValidateFlags_vpsIPMustBePublicIPv4(t *testing.T) {
	for _, ip := range []string{"10.0.0.5", "192.168.1.2", "100.64.0.1", "127.0.0.1", "::1", "2001:db8::1", "not-an-ip"} {
		if err := NewValidator(&Flags{VpsIP: ip}, t.TempDir()).ValidateFlags(); err == nil {
			t.Errorf("--vps-ip %s was accepted", ip)
		}
	}
	if err := NewValidator(&Flags{VpsIP: "203.0.113.7"}, t.TempDir()).ValidateFlags(); err != nil {
		t.Errorf("a public IPv4 address was refused: %v", err)
	}
}
