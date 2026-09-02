package production

import (
	"strings"
	"testing"
)

func TestHostUnits_secretsNotWritable(t *testing.T) {
	ssg := NewSystemdServiceGenerator("/opt/orama", "/opt/orama/.orama")
	coredns := ssg.GenerateCoreDNSService()
	if !strings.Contains(coredns, "InaccessiblePaths=/opt/orama/.orama/secrets") {
		t.Error("CoreDNS must not be able to write secrets/")
	}
	if strings.Contains(coredns, "ReadWritePaths=/opt/orama/.orama\n") {
		t.Error("CoreDNS must not have a whole-tree ReadWritePaths")
	}
	caddy := ssg.GenerateCaddyService()
	if !strings.Contains(caddy, "InaccessiblePaths=/opt/orama/.orama/secrets") {
		t.Error("Caddy must not be able to write secrets/")
	}
	gw := ssg.GenerateGatewayService()
	if !strings.Contains(gw, "ReadOnlyPaths=/opt/orama/.orama/secrets") {
		t.Error("gateway must read secrets (HMAC / encryption key)")
	}
	if strings.Contains(gw, "ReadWritePaths=/opt/orama/.orama\n") {
		t.Error("gateway must not have a whole-tree ReadWritePaths")
	}
}

func TestAnyoneClient_hasHostHardening(t *testing.T) {
	ssg := NewSystemdServiceGenerator("/opt/orama", "/opt/orama/.orama")
	unit := ssg.GenerateAnyoneClientService()
	for _, want := range []string{
		"User=debian-anon",
		"ProtectSystem=strict",
		"NoNewPrivileges=yes",
		"RestrictNamespaces=yes",
		"ProtectProc=invisible",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("anyone-client missing %q", want)
		}
	}
}

func TestServiceHardening_protectProc(t *testing.T) {
	if !strings.Contains(oramaServiceHardening, "ProtectProc=invisible") {
		t.Error("host hardening must include ProtectProc=invisible")
	}
}
