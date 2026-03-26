package tlsutil

import (
	"crypto/tls"
	"testing"
	"time"
)

func TestGetTrustedDomains(t *testing.T) {
	domains := GetTrustedDomains()

	if len(domains) == 0 {
		t.Fatal("GetTrustedDomains() returned empty slice; expected at least the default domains")
	}

	// The default list must contain *.orama.network
	found := false
	for _, d := range domains {
		if d == "*.orama.network" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GetTrustedDomains() = %v; expected to contain '*.orama.network'", domains)
	}
}

func TestShouldSkipTLSVerify_TrustedDomains(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   bool
	}{
		// Wildcard matches for *.orama.network
		{
			name:   "subdomain of orama.network",
			domain: "api.orama.network",
			want:   true,
		},
		{
			name:   "another subdomain of orama.network",
			domain: "node1.orama.network",
			want:   true,
		},
		{
			name:   "bare orama.network matches wildcard",
			domain: "orama.network",
			want:   true,
		},
		// Untrusted domains
		{
			name:   "google.com is untrusted",
			domain: "google.com",
			want:   false,
		},
		{
			name:   "example.com is untrusted",
			domain: "example.com",
			want:   false,
		},
		{
			name:   "random domain is untrusted",
			domain: "evil.example.org",
			want:   false,
		},
		{
			name:   "empty string is untrusted",
			domain: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldSkipTLSVerify(tt.domain)
			if got != tt.want {
				t.Errorf("ShouldSkipTLSVerify(%q) = %v; want %v", tt.domain, got, tt.want)
			}
		})
	}
}

func TestShouldSkipTLSVerify_WildcardMatching(t *testing.T) {
	// Verify the wildcard logic by checking that subdomains match
	// while unrelated domains do not, using the default *.orama.network entry.

	wildcardSubdomains := []string{
		"app.orama.network",
		"staging.orama.network",
		"dev.orama.network",
	}
	for _, domain := range wildcardSubdomains {
		if !ShouldSkipTLSVerify(domain) {
			t.Errorf("ShouldSkipTLSVerify(%q) = false; expected true (wildcard match)", domain)
		}
	}

	nonMatching := []string{
		"orama.com",
		"network.orama.com",
		"notorama.network",
	}
	for _, domain := range nonMatching {
		if ShouldSkipTLSVerify(domain) {
			t.Errorf("ShouldSkipTLSVerify(%q) = true; expected false (should not match wildcard)", domain)
		}
	}
}

func TestShouldSkipTLSVerify_ExactMatch(t *testing.T) {
	// The default list has *.orama.network as a wildcard, so the bare domain
	// "orama.network" should also be trusted (the implementation handles this
	// by stripping the leading dot from the suffix and comparing).
	if !ShouldSkipTLSVerify("orama.network") {
		t.Error("ShouldSkipTLSVerify(\"orama.network\") = false; expected true (exact match via wildcard)")
	}
}

func TestNewHTTPClient(t *testing.T) {
	timeout := 30 * time.Second
	client := NewHTTPClient(timeout)

	if client == nil {
		t.Fatal("NewHTTPClient() returned nil")
	}
	if client.Timeout != timeout {
		t.Errorf("NewHTTPClient() timeout = %v; want %v", client.Timeout, timeout)
	}
	if client.Transport == nil {
		t.Fatal("NewHTTPClient() returned client with nil Transport")
	}
}

func TestNewHTTPClient_DifferentTimeouts(t *testing.T) {
	timeouts := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		60 * time.Second,
	}
	for _, timeout := range timeouts {
		client := NewHTTPClient(timeout)
		if client == nil {
			t.Fatalf("NewHTTPClient(%v) returned nil", timeout)
		}
		if client.Timeout != timeout {
			t.Errorf("NewHTTPClient(%v) timeout = %v; want %v", timeout, client.Timeout, timeout)
		}
	}
}

func TestNewHTTPClientForDomain_Trusted(t *testing.T) {
	timeout := 15 * time.Second
	client := NewHTTPClientForDomain(timeout, "api.orama.network")

	if client == nil {
		t.Fatal("NewHTTPClientForDomain() returned nil for trusted domain")
	}
	if client.Timeout != timeout {
		t.Errorf("NewHTTPClientForDomain() timeout = %v; want %v", client.Timeout, timeout)
	}
	if client.Transport == nil {
		t.Fatal("NewHTTPClientForDomain() returned client with nil Transport for trusted domain")
	}
}

func TestNewHTTPClientForDomain_Untrusted(t *testing.T) {
	timeout := 15 * time.Second
	client := NewHTTPClientForDomain(timeout, "google.com")

	if client == nil {
		t.Fatal("NewHTTPClientForDomain() returned nil for untrusted domain")
	}
	if client.Timeout != timeout {
		t.Errorf("NewHTTPClientForDomain() timeout = %v; want %v", client.Timeout, timeout)
	}
	if client.Transport == nil {
		t.Fatal("NewHTTPClientForDomain() returned client with nil Transport for untrusted domain")
	}
}

func TestGetTLSConfig(t *testing.T) {
	config := GetTLSConfig()

	if config == nil {
		t.Fatal("GetTLSConfig() returned nil")
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("GetTLSConfig() MinVersion = %v; want %v (TLS 1.2)", config.MinVersion, tls.VersionTLS12)
	}
}

func TestGetTLSConfig_ReturnsNewInstance(t *testing.T) {
	config1 := GetTLSConfig()
	config2 := GetTLSConfig()

	if config1 == config2 {
		t.Error("GetTLSConfig() returned the same pointer twice; expected distinct instances")
	}
}
