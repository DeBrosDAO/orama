package storageclient

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsPublic_refusesInwardRanges(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.2": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false, "fe80::1": false, "fc00::1": false,
	} {
		if got := IsPublic(net.ParseIP(addr)); got != want {
			t.Errorf("%s: got %t want %t", addr, got, want)
		}
	}
}

func TestPublicHTTPClient_refusesALoopbackProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := PublicHTTPClient(time.Second).Get(srv.URL)
	if !errors.Is(err, ErrNotPublic) {
		t.Fatalf("got %v, want ErrNotPublic", err)
	}
}
