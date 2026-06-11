package turn

import (
	"bytes"
	"crypto/tls"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// feat-124: the stealth TURNS endpoint is an SNI-router passthrough — the TURN
// server terminates TLS for both the primary TURN domain and a neutral stealth
// domain, selecting the cert by ClientHello SNI. These pin: per-SNI selection
// (incl. empty SNI, case-insensitivity), partial-config startup failure, and
// the missing stealth-cert startup failure (no silent fallback).

const (
	stealthTestDomain = "cdn-a1b2c3d4e5f6.orama-devnet.network"
	turnTestDomain    = "turn.orama-devnet.network"
)

func writeNamedCert(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()
	certPath = filepath.Join(dir, name+".pem")
	keyPath = filepath.Join(dir, name+".key.pem")
	if err := GenerateSelfSignedCert(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatalf("GenerateSelfSignedCert(%s): %v", name, err)
	}
	return certPath, keyPath
}

func certLeafForSNI(t *testing.T, getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error), serverName string) []byte {
	t.Helper()
	cert, err := getCert(&tls.ClientHelloInfo{ServerName: serverName})
	if err != nil {
		t.Fatalf("GetCertificate(%q): %v", serverName, err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("GetCertificate(%q) returned an empty certificate", serverName)
	}
	return cert.Certificate[0]
}

func TestGetCertificate_stealthSNISelectsStealthCert(t *testing.T) {
	dir := t.TempDir()
	primaryCert, primaryKey := writeNamedCert(t, dir, "primary")
	stealthCert, stealthKey := writeNamedCert(t, dir, "stealth")

	primary, err := newCertReloader(primaryCert, primaryKey, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader(primary): %v", err)
	}
	stealth, err := newCertReloader(stealthCert, stealthKey, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader(stealth): %v", err)
	}

	getCert := newGetCertificate(stealthTestDomain, primary, stealth)

	wantPrimary := leafDER(t, primary)
	wantStealth := leafDER(t, stealth)
	if bytes.Equal(wantPrimary, wantStealth) {
		t.Fatal("test setup error: primary and stealth certs must be distinct")
	}

	tests := []struct {
		name       string
		serverName string
		want       []byte
	}{
		{"stealth SNI selects stealth cert", stealthTestDomain, wantStealth},
		{"stealth SNI is case-insensitive", strings.ToUpper(stealthTestDomain), wantStealth},
		{"turn domain SNI selects primary cert", turnTestDomain, wantPrimary},
		{"empty SNI selects primary cert", "", wantPrimary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := certLeafForSNI(t, getCert, tt.serverName)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ServerName=%q served the wrong certificate", tt.serverName)
			}
		})
	}
}

func TestGetCertificate_stealthDisabledAlwaysPrimary(t *testing.T) {
	dir := t.TempDir()
	primaryCert, primaryKey := writeNamedCert(t, dir, "primary")
	primary, err := newCertReloader(primaryCert, primaryKey, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader(primary): %v", err)
	}

	// Stealth disabled (nil reloader): every SNI — including a string that looks
	// like a stealth host — must serve the primary cert unchanged.
	getCert := newGetCertificate("", primary, nil)
	want := leafDER(t, primary)

	for _, serverName := range []string{"", turnTestDomain, stealthTestDomain} {
		if got := certLeafForSNI(t, getCert, serverName); !bytes.Equal(got, want) {
			t.Errorf("ServerName=%q must serve the primary cert when stealth is disabled", serverName)
		}
	}
}

func baseStealthConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	primaryCert, primaryKey := writeNamedCert(t, dir, "primary")
	return &Config{
		ListenAddr:      "127.0.0.1:0",
		TURNSListenAddr: "127.0.0.1:0",
		TLSCertPath:     primaryCert,
		TLSKeyPath:      primaryKey,
		PublicIP:        "127.0.0.1",
		Realm:           "orama-devnet.network",
		AuthSecret:      "test-secret-key",
		RelayPortStart:  49152,
		RelayPortEnd:    50000,
		Namespace:       "test-ns",
	}
}

func TestServer_partialStealthConfigFails(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(c *Config)
		wantMissing []string
	}{
		{
			name:        "only stealth_domain set",
			mutate:      func(c *Config) { c.StealthDomain = stealthTestDomain },
			wantMissing: []string{"tls_stealth_cert_path", "tls_stealth_key_path"},
		},
		{
			name:        "domain and cert set, key missing",
			mutate:      func(c *Config) { c.StealthDomain = stealthTestDomain; c.TLSStealthCertPath = "/tmp/x.pem" },
			wantMissing: []string{"tls_stealth_key_path"},
		},
		{
			name:        "only cert path set",
			mutate:      func(c *Config) { c.TLSStealthCertPath = "/tmp/x.pem" },
			wantMissing: []string{"stealth_domain", "tls_stealth_key_path"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseStealthConfig(t)
			tt.mutate(cfg)

			srv, err := NewServer(cfg, zap.NewNop())
			if err == nil {
				srv.Close()
				t.Fatal("expected startup to fail on partial stealth config")
			}
			for _, field := range tt.wantMissing {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("error must name the missing field %q; got: %v", field, err)
				}
			}
		})
	}
}

func TestServer_missingStealthCertFails(t *testing.T) {
	cfg := baseStealthConfig(t)
	cfg.StealthDomain = stealthTestDomain
	cfg.TLSStealthCertPath = filepath.Join(t.TempDir(), "absent-cert.pem")
	cfg.TLSStealthKeyPath = filepath.Join(t.TempDir(), "absent-key.pem")

	srv, err := NewServer(cfg, zap.NewNop())
	if err == nil {
		srv.Close()
		t.Fatal("expected startup to fail when the stealth cert file is absent")
	}
	if !strings.Contains(err.Error(), cfg.TLSStealthCertPath) {
		t.Errorf("error must name the missing stealth cert path %q; got: %v", cfg.TLSStealthCertPath, err)
	}
}

func TestServer_fullStealthConfigStarts(t *testing.T) {
	cfg := baseStealthConfig(t)
	dir := t.TempDir()
	stealthCert, stealthKey := writeNamedCert(t, dir, "stealth")
	cfg.StealthDomain = stealthTestDomain
	cfg.TLSStealthCertPath = stealthCert
	cfg.TLSStealthKeyPath = stealthKey

	srv, err := NewServer(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("expected startup to succeed with full stealth config: %v", err)
	}
	defer srv.Close()
	if srv.stealthCertReloader == nil {
		t.Error("stealthCertReloader must be set when stealth is fully configured")
	}
}
