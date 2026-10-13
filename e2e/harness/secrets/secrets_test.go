package secrets

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRedact_knownValuesAndPatterns(t *testing.T) {
	r := FromEnv(lookupFrom(map[string]string{"HCLOUD_TOKEN": "hcloud-secret-value-123"}))
	in := strings.Join([]string{
		"token hcloud-secret-value-123 used",
		`Authorization: Bearer abcdefghijklmnop`,
		`{"access_token":"xyz-abc-123","refresh_token":"dv1_zzzz","ok":true}`,
		"GET /v1/x?token=supersecret&y=1",
		"jwt eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweDEifQ.c2lnbmF0dXJl end",
		"key orama_live_AbC123_Zz9 end",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----",
	}, "\n")
	out := r.Redact(in)
	for _, leaked := range []string{"hcloud-secret-value-123", "abcdefghijklmnop", "xyz-abc-123", "dv1_zzzz",
		"supersecret", "eyJhbGciOiJFZERTQSJ9", "orama_live_AbC123_Zz9", "AAAA"} {
		if strings.Contains(out, leaked) {
			t.Errorf("redacted output still contains %q:\n%s", leaked, out)
		}
	}
	for _, kept := range []string{`"ok":true`, "&y=1", "Authorization: ", `"access_token":"`} {
		if !strings.Contains(out, kept) {
			t.Errorf("redaction removed context %q:\n%s", kept, out)
		}
	}
}

func TestRedact_shortValuesIgnored(t *testing.T) {
	r := NewRedactor("abc", "", "   ")
	if got := r.Redact("abc def"); got != "abc def" {
		t.Fatalf("short value was redacted: %q", got)
	}
}

func TestRedact_nilRedactorStillAppliesPatterns(t *testing.T) {
	var r *Redactor
	if got := r.Redact(`{"api_key":"k-123456789"}`); strings.Contains(got, "k-123456789") {
		t.Fatalf("nil redactor leaked: %q", got)
	}
}

func TestRedact_longestValueFirst(t *testing.T) {
	r := NewRedactor("secret-part", "secret-part-and-more")
	if got := r.Redact("x secret-part-and-more y"); got != "x "+Mask+" y" {
		t.Fatalf("got %q", got)
	}
}

func TestCheckAgentSockNotRealWallet_cases(t *testing.T) {
	home := "/home/owner"
	cases := []struct {
		name, sock string
		wantErr    bool
	}{
		{"throwaway", "/tmp/e2e-ab/agent.sock", false},
		{"empty", "", true},
		{"relative", "agent.sock", true},
		{"real wallet", "/home/owner/.rootwallet/agent.sock", true},
		{"real wallet dir itself", "/home/owner/.rootwallet", true},
		{"dotdot into wallet", "/home/owner/x/../.rootwallet/agent.sock", true},
		{"sibling name", "/home/owner/.rootwallet-e2e/agent.sock", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckAgentSockNotRealWallet(c.sock, home)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
	if err := CheckAgentSockNotRealWallet("", home); !errors.Is(err, ErrAgentSockEmpty) {
		t.Fatalf("empty sock: got %v", err)
	}
	if err := CheckAgentSockNotRealWallet("/tmp/a.sock", ""); err == nil {
		t.Fatal("unknown home must refuse")
	}
}

func TestCheckAgentSockOutsideHome_cases(t *testing.T) {
	home := "/Users/owner"
	if err := CheckAgentSockOutsideHome("/tmp/run/agent.sock", home); err != nil {
		t.Fatalf("temp sock refused: %v", err)
	}
	if err := CheckAgentSockOutsideHome(filepath.Join(home, "dev/run/agent.sock"), home); err == nil {
		t.Fatal("sock inside the real home was accepted")
	}
	if err := CheckAgentSockOutsideHome("  ", home); !errors.Is(err, ErrAgentSockEmpty) {
		t.Fatalf("blank sock: got %v", err)
	}
}

func TestRealHome_returnsAbsolutePath(t *testing.T) {
	h, err := RealHome()
	if err != nil {
		t.Fatalf("RealHome: %v", err)
	}
	if !filepath.IsAbs(h) {
		t.Fatalf("home %q is not absolute", h)
	}
}

func TestMissingEnv_namesOnlySorted(t *testing.T) {
	got := MissingEnv([]string{"Z_VAR", "A_VAR", "SET"}, lookupFrom(map[string]string{"SET": "v", "Z_VAR": ""}))
	if strings.Join(got, ",") != "A_VAR,Z_VAR" {
		t.Fatalf("got %v", got)
	}
	if got := MissingEnv(nil, lookupFrom(nil)); len(got) != 0 {
		t.Fatalf("no required names: got %v", got)
	}
}
