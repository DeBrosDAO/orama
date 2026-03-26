package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withTempHome(t *testing.T) func() {
	d := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", d)
	return func() { os.Setenv("HOME", oldHome) }
}

func TestCredentialStoreCRUD(t *testing.T) {
	defer withTempHome(t)()
	store, err := LoadCredentials()
	if err != nil { t.Fatal(err) }
	if len(store.Gateways) != 0 { t.Fatalf("expected empty") }

	creds := &Credentials{APIKey: "ak_1:ns", Namespace: "ns", IssuedAt: time.Now()}
	store.SetCredentialsForGateway("http://gw", creds)
	if err := store.SaveCredentials(); err != nil { t.Fatal(err) }

	store2, err := LoadCredentials()
	if err != nil { t.Fatal(err) }
	c, ok := store2.GetCredentialsForGateway("http://gw")
	if !ok || c.APIKey != "ak_1:ns" { t.Fatalf("not found") }

	store2.RemoveCredentialsForGateway("http://gw")
	if err := store2.SaveCredentials(); err != nil { t.Fatal(err) }

	path, _ := GetCredentialsPath()
	if _, err := os.Stat(filepath.Dir(path)); err != nil { t.Fatal(err) }
}

func TestIsExpiredAndValid(t *testing.T) {
	c := &Credentials{APIKey: "ak", Namespace: "ns", ExpiresAt: time.Now().Add(-time.Hour)}
	if !c.IsExpired() { t.Fatalf("expected expired") }
	if c.IsValid() { t.Fatalf("expired should be invalid") }
	c.ExpiresAt = time.Time{}
	if !c.IsValid() { t.Fatalf("no expiry should be valid") }
}
