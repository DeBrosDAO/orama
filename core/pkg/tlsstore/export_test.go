package tlsstore

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const exportBase = "example.com"

// testPair is a self-signed certificate for names, valid until notAfter.
func testPair(t *testing.T, notAfter time.Time, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

// storeWildcard seals and stores a pair the way Caddy does, under issuer.
func storeWildcard(t *testing.T, s *Store, k Keys, issuer string, certPEM, keyPEM []byte) {
	t.Helper()
	dir := "certificates/" + issuer + "/wildcard_." + exportBase + "/wildcard_." + exportBase
	for path, data := range map[string][]byte{dir + ".crt": certPEM, dir + ".key": keyPEM} {
		sealed, err := Seal(k.Seal, path, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(context.Background(), path, sealed); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestExporter(t *testing.T) (*Exporter, *Store, Keys) {
	t.Helper()
	s, db := newTestStore(t)
	k := vectorKeys(t)
	dir := t.TempDir()
	e := NewExporter(db, k.Seal, exportBase, filepath.Join(dir, "tls", "wildcard.crt"), filepath.Join(dir, "tls", "wildcard.key"))
	return e, s, k
}

func TestExport_writesTheStoredWildcardOnce(t *testing.T) {
	e, s, k := newTestExporter(t)
	cert, key := testPair(t, time.Now().Add(60*24*time.Hour), "*."+exportBase)
	storeWildcard(t, s, k, "acme-v02.api.letsencrypt.org-directory", cert, key)

	wrote, err := e.Export(context.Background())
	if err != nil || !wrote {
		t.Fatalf("Export = %v, %v", wrote, err)
	}
	if got, _ := os.ReadFile(e.certPath); !bytes.Equal(got, cert) {
		t.Error("the certificate file is not the stored certificate")
	}
	if got, _ := os.ReadFile(e.keyPath); !bytes.Equal(got, key) {
		t.Error("the key file is not the stored key")
	}
	if st, _ := os.Stat(e.keyPath); st.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", st.Mode().Perm())
	}
	if wrote, err := e.Export(context.Background()); err != nil || wrote {
		t.Fatalf("an unchanged export rewrote the files: %v, %v", wrote, err)
	}
}

// When the CA was switched, both issuers' certificates are stored; the one
// stored last is the one Caddy renews.
func TestExport_takesTheNewestIssuer(t *testing.T) {
	e, s, k := newTestExporter(t)
	now := time.UnixMilli(1_790_000_000_000)
	s.now = func() time.Time { return now }
	oldCert, oldKey := testPair(t, time.Now().Add(80*24*time.Hour), "*."+exportBase)
	storeWildcard(t, s, k, "acme-staging-v02.api.letsencrypt.org-directory", oldCert, oldKey)
	now = now.Add(time.Hour)
	newCert, newKey := testPair(t, time.Now().Add(60*24*time.Hour), "*."+exportBase)
	storeWildcard(t, s, k, "acme-v02.api.letsencrypt.org-directory", newCert, newKey)

	if _, err := e.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(e.certPath); !bytes.Equal(got, newCert) {
		t.Error("the export did not take the most recently stored certificate")
	}
}

func TestExport_nothingStoredYet(t *testing.T) {
	e, _, _ := newTestExporter(t)
	if _, err := e.Export(context.Background()); !IsNotExist(err) {
		t.Fatalf("Export with an empty store: %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(e.certPath); !os.IsNotExist(err) {
		t.Error("a certificate file was written from an empty store")
	}
}

func TestExport_refusesWhatTURNCouldNotServe(t *testing.T) {
	future := time.Now().Add(60 * 24 * time.Hour)
	goodCert, goodKey := testPair(t, future, "*."+exportBase)
	_, otherKey := testPair(t, future, "*."+exportBase)
	expiredCert, expiredKey := testPair(t, time.Now().Add(-time.Hour), "*."+exportBase)
	apexCert, apexKey := testPair(t, future, exportBase)
	cases := map[string][2][]byte{
		"key of another certificate":    {goodCert, otherKey},
		"expired":                       {expiredCert, expiredKey},
		"does not cover a single label": {apexCert, apexKey},
		"not a certificate":             {[]byte("garbage"), goodKey},
	}
	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			e, s, k := newTestExporter(t)
			storeWildcard(t, s, k, "acme-v02.api.letsencrypt.org-directory", pair[0], pair[1])
			if _, err := e.Export(context.Background()); err == nil {
				t.Fatal("Export accepted it")
			}
			if _, err := os.Stat(e.certPath); !os.IsNotExist(err) {
				t.Error("a refused certificate was written")
			}
		})
	}
}

func TestExport_sealedUnderAnotherCluster(t *testing.T) {
	e, s, _ := newTestExporter(t)
	other, err := KeysFromClusterSecret("another cluster")
	if err != nil {
		t.Fatal(err)
	}
	cert, key := testPair(t, time.Now().Add(time.Hour), "*."+exportBase)
	storeWildcard(t, s, other, "acme-v02.api.letsencrypt.org-directory", cert, key)
	if _, err := e.Export(context.Background()); err == nil {
		t.Fatal("Export opened values sealed under another cluster's key")
	}
}

func TestExport_noBaseDomain(t *testing.T) {
	_, db := newTestStore(t)
	e := NewExporter(db, vectorKeys(t).Seal, "", "/nonexistent/c", "/nonexistent/k")
	if _, err := e.Export(context.Background()); err == nil {
		t.Fatal("Export ran with no base domain")
	}
}
