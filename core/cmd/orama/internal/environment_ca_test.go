package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCAFile(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "staging root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// restoreDefaultTransport undoes InstallScopedRoots for the rest of the package.
func restoreDefaultTransport(t *testing.T) {
	t.Helper()
	tr := http.DefaultTransport.(*http.Transport)
	saved := tr.TLSClientConfig
	t.Cleanup(func() { tr.TLSClientConfig = saved })
}

func TestSetEnvironmentCA_recordsTheAbsolutePath(t *testing.T) {
	defer writeTestConfig(t, defaultTestConfig())()
	ca := writeCAFile(t)

	if err := SetEnvironmentCA("devnet", ca); err != nil {
		t.Fatalf("SetEnvironmentCA: %v", err)
	}
	env, err := GetEnvironmentByName("devnet")
	if err != nil {
		t.Fatal(err)
	}
	if env.CAFile != ca || !filepath.IsAbs(env.CAFile) {
		t.Errorf("CAFile = %q, want %q", env.CAFile, ca)
	}
}

// A typo in the path fails when it is typed, not on the next command.
func TestSetEnvironmentCA_refusesAFileItCannotUse(t *testing.T) {
	defer writeTestConfig(t, defaultTestConfig())()
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{junk, filepath.Join(t.TempDir(), "absent.pem")} {
		if err := SetEnvironmentCA("devnet", file); err == nil {
			t.Errorf("SetEnvironmentCA(%s) accepted", file)
		}
	}
	env, _ := GetEnvironmentByName("devnet")
	if env.CAFile != "" {
		t.Errorf("a refused CA was saved: %q", env.CAFile)
	}
	if err := SetEnvironmentCA("nowhere", writeCAFile(t)); err == nil {
		t.Error("a CA was set on an environment that does not exist")
	}
}

func TestAddEnvironment_updateKeepsTheCA(t *testing.T) {
	defer writeTestConfig(t, defaultTestConfig())()
	ca := writeCAFile(t)
	if err := SetEnvironmentCA("devnet", ca); err != nil {
		t.Fatal(err)
	}
	if err := AddEnvironment("devnet", "https://orama-devnet.network", "renamed"); err != nil {
		t.Fatal(err)
	}
	if env, _ := GetEnvironmentByName("devnet"); env.CAFile != ca {
		t.Errorf("updating the environment dropped its CA: %q", env.CAFile)
	}
}

func TestTrustEnvironmentCAs_missingFileNamesTheEnvironment(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Environments[1].CAFile = filepath.Join(t.TempDir(), "gone.pem")
	defer writeTestConfig(t, cfg)()
	restoreDefaultTransport(t)

	err := TrustEnvironmentCAs()
	if err == nil {
		t.Fatal("a missing CA file was ignored")
	}
	if !strings.Contains(err.Error(), `"devnet"`) || !strings.Contains(err.Error(), "--ca-file") {
		t.Errorf("the error does not say which environment or how to fix it: %v", err)
	}
}

func TestTrustEnvironmentCAs_installsTheScopedVerifier(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Environments[1].CAFile = writeCAFile(t)
	defer writeTestConfig(t, cfg)()
	restoreDefaultTransport(t)

	if err := TrustEnvironmentCAs(); err != nil {
		t.Fatalf("TrustEnvironmentCAs: %v", err)
	}
	tc := http.DefaultTransport.(*http.Transport).TLSClientConfig
	if tc == nil || tc.VerifyConnection == nil {
		t.Fatal("the default transport does not verify with the scoped roots")
	}
}

// The CA was trusted for one domain; moving the environment must not carry it
// to another.
func TestAddEnvironment_newHostDropsTheCA(t *testing.T) {
	defer writeTestConfig(t, defaultTestConfig())()
	if err := SetEnvironmentCA("devnet", writeCAFile(t)); err != nil {
		t.Fatal(err)
	}
	if err := AddEnvironment("devnet", "https://elsewhere.example", "moved"); err != nil {
		t.Fatal(err)
	}
	if env, _ := GetEnvironmentByName("devnet"); env.CAFile != "" {
		t.Errorf("the CA followed the environment to a new host: %q", env.CAFile)
	}
}
