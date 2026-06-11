package push

import (
	"context"
	"errors"
	"net"
	"testing"
)

// SSRF guard for tenant push base URLs. These pin: literal internal/reserved IPs
// are rejected, the cloud-metadata IP is rejected, legit external hosts pass,
// and a hostname that RESOLVES to an internal address is rejected (the DNS
// vector) while a public-resolving host passes.

func TestCheckBaseURLSyntax(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"", false},                          // empty = use default
		{"https://push.example.com", false},  // public host
		{"http://push.example.com:8090", false},
		{"https://1.1.1.1", false},           // public literal IP
		{"https://[2606:4700:4700::1111]", false}, // public v6
		{"ftp://push.example.com", true},     // bad scheme
		{"notaurl", true},                    // no scheme/host
		{"http://", true},                    // missing host
		{"http://169.254.169.254", true},     // cloud metadata (link-local)
		{"http://127.0.0.1", true},           // loopback
		{"http://127.0.0.1:8090", true},      // loopback + port
		{"http://10.0.0.5", true},            // RFC1918 (WireGuard mesh)
		{"http://192.168.1.1", true},         // RFC1918
		{"http://172.16.0.1", true},          // RFC1918
		{"http://100.64.0.1", true},          // CGNAT
		{"http://0.0.0.0", true},             // unspecified
		{"http://[::1]", true},               // v6 loopback
		{"http://[fd00::1]", true},           // v6 ULA
		{"http://[64:ff9b::a00:5]", true},    // NAT64-embedded 10.0.0.5
		{"http://0x7f000001", true},          // hex-encoded 127.0.0.1
		{"http://2130706433", true},          // decimal-encoded 127.0.0.1
		{"http://0177.0.0.1", true},          // octal-encoded 127.0.0.1
	}
	for _, tc := range cases {
		err := CheckBaseURLSyntax(tc.url)
		if tc.wantErr && err == nil {
			t.Errorf("CheckBaseURLSyntax(%q) = nil; want error", tc.url)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("CheckBaseURLSyntax(%q) = %v; want nil", tc.url, err)
		}
	}
}

func TestIsReservedIP(t *testing.T) {
	reserved := []string{
		"127.0.0.1", "169.254.169.254", "10.0.0.1", "172.16.5.5", "192.168.0.1",
		"100.64.0.1", "100.100.100.200", "0.0.0.0", "224.0.0.1", "::1", "fe80::1",
		"fd00::1", "ff02::1",
		"64:ff9b::a00:1",     // NAT64-embedded 10.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64-embedded 169.254.169.254 (metadata)
	}
	public := []string{"1.1.1.1", "8.8.8.8", "203.0.113.10", "2606:4700:4700::1111"}
	for _, s := range reserved {
		if ip := net.ParseIP(s); !isReservedIP(ip) {
			t.Errorf("isReservedIP(%s) = false; want true (reserved)", s)
		}
	}
	for _, s := range public {
		if ip := net.ParseIP(s); isReservedIP(ip) {
			t.Errorf("isReservedIP(%s) = true; want false (public)", s)
		}
	}
	if !isReservedIP(nil) {
		t.Error("isReservedIP(nil) must be true (unparseable → unsafe)")
	}
}

func TestIsInternalBaseURL(t *testing.T) {
	internal := []string{
		"http://10.0.0.5", "http://169.254.169.254",
		"https://127.0.0.1:8090", "http://[::1]", "http://192.168.1.1",
		"http://[64:ff9b::a00:5]", // NAT64
		"http://0x7f000001",       // hex-encoded loopback
		"http://2130706433",       // decimal-encoded loopback
		"http://0177.0.0.1",       // octal-encoded loopback
	}
	notInternal := []string{
		"https://push.example.com", // hostname → false (the set path resolves it)
		"https://1.1.1.1",          // public literal IP
		"ns-A-url",                 // malformed placeholder → must NOT be dropped
		"v1", "", "not a url",
	}
	for _, s := range internal {
		if !IsInternalBaseURL(s) {
			t.Errorf("IsInternalBaseURL(%q) = false; want true (internal literal IP)", s)
		}
	}
	for _, s := range notInternal {
		if IsInternalBaseURL(s) {
			t.Errorf("IsInternalBaseURL(%q) = true; want false", s)
		}
	}
}

func TestCheckBaseURLResolvable(t *testing.T) {
	orig := lookupIP
	defer func() { lookupIP = orig }()

	t.Run("hostname resolving to internal is rejected", func(t *testing.T) {
		lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.7")}, nil // points at the mesh
		}
		if err := CheckBaseURLResolvable(context.Background(), "https://evil.example.com"); err == nil {
			t.Fatal("expected rejection of a host resolving to an internal address")
		}
	})

	t.Run("hostname resolving to public is allowed", func(t *testing.T) {
		lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.50")}, nil
		}
		if err := CheckBaseURLResolvable(context.Background(), "https://push.example.com"); err != nil {
			t.Fatalf("public-resolving host should pass: %v", err)
		}
	})

	t.Run("any internal IP among results is rejected", func(t *testing.T) {
		lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.50"), net.ParseIP("127.0.0.1")}, nil
		}
		if err := CheckBaseURLResolvable(context.Background(), "https://mixed.example.com"); err == nil {
			t.Fatal("a host resolving to ANY internal address must be rejected")
		}
	})

	t.Run("resolution failure is allowed (fail open)", func(t *testing.T) {
		lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
			return nil, errors.New("nxdomain")
		}
		if err := CheckBaseURLResolvable(context.Background(), "https://unresolvable.example.com"); err != nil {
			t.Fatalf("an unresolvable host should fail open (be allowed); got %v", err)
		}
	})

	t.Run("literal internal IP rejected without DNS", func(t *testing.T) {
		lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
			t.Fatal("DNS must not be consulted for a literal IP host")
			return nil, nil
		}
		if err := CheckBaseURLResolvable(context.Background(), "http://169.254.169.254"); err == nil {
			t.Fatal("literal metadata IP must be rejected")
		}
	})

	t.Run("empty is allowed", func(t *testing.T) {
		if err := CheckBaseURLResolvable(context.Background(), ""); err != nil {
			t.Fatalf("empty base_url should pass: %v", err)
		}
	})
}
