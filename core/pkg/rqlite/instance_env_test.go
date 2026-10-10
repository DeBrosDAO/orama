package rqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func writeInstanceEnv(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := InstanceEnvFile(dir, "anchat")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstanceAddrFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"WireGuard bind", "# generated\nNODE_ID=n1\nHTTP_ADDR=10.0.0.1:10200\nHTTP_ADV_ADDR=10.0.0.1:10200\n", "10.0.0.1:10200", false},
		// Spawned before the WireGuard bind: rqlite.env is only rewritten on
		// respawn, and the wildcard listener is reachable on the advertise
		// address.
		{"legacy wildcard bind", "HTTP_ADDR=0.0.0.0:10200\nHTTP_ADV_ADDR=10.0.0.4:10200\n", "10.0.0.4:10200", false},
		{"empty-host bind", "HTTP_ADDR=:10200\nHTTP_ADV_ADDR=10.0.0.4:10200\n", "10.0.0.4:10200", false},
		{"no HTTP_ADDR", "NODE_ID=n1\n", "", true},
		{"wildcard without advertise", "HTTP_ADDR=0.0.0.0:10200\n", "", true},
		{"wildcard advertise", "HTTP_ADDR=0.0.0.0:10200\nHTTP_ADV_ADDR=0.0.0.0:10200\n", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := InstanceAddrFromEnv(writeInstanceEnv(t, tc.body))
			if !ok {
				t.Fatal("existing env file reported absent")
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestInstanceAddrFromEnv_missingFileIsAbsent(t *testing.T) {
	_, ok, err := InstanceAddrFromEnv(filepath.Join(t.TempDir(), "absent.env"))
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want absent without error", ok, err)
	}
}

func TestInstanceEndpointFromEnv_carriesCredentials(t *testing.T) {
	ep, ok, err := InstanceEndpointFromEnv(writeInstanceEnv(t, "HTTP_ADDR=10.0.0.1:10200\n"), testUser, testPass)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ep.HostPort() != "10.0.0.1:10200" || ep.Username != testUser || ep.Password != testPass {
		t.Fatalf("got %v", ep)
	}
	if _, _, err := InstanceEndpointFromEnv(writeInstanceEnv(t, "HTTP_ADDR=10.0.0.1:10200\n"), testUser, ""); err == nil {
		t.Fatal("missing password accepted")
	}
}
