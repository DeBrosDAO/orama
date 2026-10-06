package enroll

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

func TestValidate_requiresTheConsoleCode(t *testing.T) {
	f := Flags{NodeIP: "203.0.113.10", Token: "tok", GatewayURL: "https://gw.example"}
	err := f.validate()
	if err == nil {
		t.Fatal("missing --code was accepted — the agent no longer serves the code")
	}
	if got := err.Error(); got == "" {
		t.Fatal("empty error")
	}

	f.Code = "a1b2c3d4e5f60718293a"
	if err := f.validate(); err != nil {
		t.Fatalf("valid flags refused: %v", err)
	}
}

// The invite token is a credential; the CLI used to send it to a plain-http
// gateway (stagenet e2e, 2026-09-30).
func TestValidate_gatewayMustBeHTTPS(t *testing.T) {
	base := Flags{NodeIP: "203.0.113.10", Code: "a1b2c3d4e5f60718293a", Token: "tok"}
	for gw, ok := range map[string]bool{
		"https://gw.example":      true,
		"https://gw.example:8443": true,
		"http://gw.example":       false,
		"ftp://gw.example":        false,
		"gw.example":              false,
		"https://":                false,
		"://bad":                  false,
	} {
		f := base
		f.GatewayURL = gw
		err := f.validate()
		if (err == nil) != ok {
			t.Errorf("--gateway %q: err %v, want ok=%v", gw, err, ok)
		}
		if err != nil && clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("--gateway %q: exit %d, want the usage exit", gw, clierr.CodeOf(err))
		}
	}
}
