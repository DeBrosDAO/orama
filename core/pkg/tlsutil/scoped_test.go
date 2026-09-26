package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// privateCA is a CA and one server certificate for dnsName, as a cluster on a
// private or staging CA has.
type privateCA struct {
	caFile string
	server tls.Certificate
}

func newPrivateCA(t *testing.T, dnsName string) privateCA {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{dnsName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return privateCA{caFile: caFile, server: tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}
}

func resetScopedRoots(t *testing.T) {
	t.Helper()
	scopedMu.Lock()
	scopedRoots = map[string]*x509.CertPool{}
	scopedMu.Unlock()
	t.Cleanup(func() {
		scopedMu.Lock()
		scopedRoots = map[string]*x509.CertPool{}
		scopedMu.Unlock()
	})
}

// handshake connects to a TLS server presenting ca's certificate, asking for
// serverName, with the client config the scoped roots produce.
func handshake(t *testing.T, ca privateCA, serverName string) error {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.server}}
	srv.StartTLS()
	defer srv.Close()

	cfg := withScopedRoots(&tls.Config{MinVersion: tls.VersionTLS12})
	cfg.ServerName = serverName
	conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), cfg)
	if err != nil {
		return err
	}
	return conn.Close()
}

func TestWithScopedRoots_trustsTheCAForItsDomain(t *testing.T) {
	resetScopedRoots(t)
	ca := newPrivateCA(t, "ns-app.stagenet.example")
	if err := TrustCAForDomain("stagenet.example", ca.caFile); err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, ca, "ns-app.stagenet.example"); err != nil {
		t.Fatalf("a certificate from the environment's CA, for a name under its domain, was refused: %v", err)
	}
}

// The CA is the environment's, not the process's: a certificate it signed
// for any other name must not verify.
func TestWithScopedRoots_doesNotTrustTheCAElsewhere(t *testing.T) {
	resetScopedRoots(t)
	ca := newPrivateCA(t, "bank.example")
	if err := TrustCAForDomain("stagenet.example", ca.caFile); err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, ca, "bank.example"); err == nil {
		t.Fatal("the environment's CA vouched for a host outside its domain")
	}
}

// A name that merely ends with the domain's text is not under it.
func TestWithScopedRoots_matchesWholeLabels(t *testing.T) {
	resetScopedRoots(t)
	ca := newPrivateCA(t, "evilstagenet.example")
	if err := TrustCAForDomain("stagenet.example", ca.caFile); err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, ca, "evilstagenet.example"); err == nil {
		t.Fatal("evilstagenet.example was treated as under stagenet.example")
	}
}

func TestWithScopedRoots_hostnameMustMatchTheCertificate(t *testing.T) {
	resetScopedRoots(t)
	ca := newPrivateCA(t, "a.stagenet.example")
	if err := TrustCAForDomain("stagenet.example", ca.caFile); err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, ca, "b.stagenet.example"); err == nil {
		t.Fatal("a certificate for a.stagenet.example verified as b.stagenet.example")
	}
}

func TestWithScopedRoots_unchangedWithoutScopedRoots(t *testing.T) {
	resetScopedRoots(t)
	base := &tls.Config{MinVersion: tls.VersionTLS12}
	if got := withScopedRoots(base); got != base || got.InsecureSkipVerify {
		t.Fatal("with no scoped roots the config must be returned untouched")
	}
	ca := newPrivateCA(t, "x.stagenet.example")
	if err := handshake(t, ca, "x.stagenet.example"); err == nil {
		t.Fatal("an unknown CA verified with no scoped roots configured")
	}
}

func TestTrustCAForDomain_refusesBadInput(t *testing.T) {
	resetScopedRoots(t)
	ca := newPrivateCA(t, "x.example")
	notPEM := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(notPEM, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ domain, file string }{
		"no domain":    {"", ca.caFile},
		"missing file": {"x.example", filepath.Join(t.TempDir(), "absent.pem")},
		"not PEM":      {"x.example", notPEM},
	} {
		if err := TrustCAForDomain(tc.domain, tc.file); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if hasScopedRoots() {
		t.Error("a refused CA was registered")
	}
}

func TestVerifyScoped_refusesWhatItCannotCheck(t *testing.T) {
	if err := verifyScoped(tls.ConnectionState{ServerName: "x.example"}, nil); err == nil {
		t.Error("no certificate was accepted")
	}
	ca := newPrivateCA(t, "x.example")
	leaf, _ := x509.ParseCertificate(ca.server.Certificate[0])
	if err := verifyScoped(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}, nil); err == nil {
		t.Error("a connection with no server name was accepted")
	}
}
