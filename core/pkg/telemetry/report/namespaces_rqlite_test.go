package report

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

var testCreds = rqlite.Endpoint{Host: "10.0.0.1", Port: 10100, Username: "orama", Password: "0123456789abcdef"}

// A namespace instance is probed at the HTTP_ADDR it binds, with the cluster
// credentials — not at localhost:<port>, where it does not listen.
func TestNamespaceRQLiteBase_usesBindAddressWithCredentials(t *testing.T) {
	got, err := namespaceRQLiteBase(nsInfo{name: "anchat", portBase: 10200, rqliteAddr: "10.0.0.1:10200"}, testCreds, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://orama:0123456789abcdef@10.0.0.1:10200" {
		t.Fatalf("got %q", got)
	}
}

// Why the rqlite checks could not run is reported, not swallowed.
func TestNamespaceRQLiteBase_errors(t *testing.T) {
	if _, err := namespaceRQLiteBase(nsInfo{rqliteAddr: "10.0.0.1:10200"}, rqlite.Endpoint{}, errors.New("node.yaml unreadable")); err == nil {
		t.Error("a credentials error was dropped")
	}
	if _, err := namespaceRQLiteBase(nsInfo{rqliteAddr: "0.0.0.0:10200"}, testCreds, nil); err == nil {
		t.Error("a wildcard HTTP_ADDR was accepted")
	}

	r := collectNamespaceReport(nsInfo{name: "anchat", portBase: 1}, "", errors.New("node.yaml unreadable"))
	if r.RQLiteUp || !strings.Contains(r.RQLiteError, "node.yaml unreadable") {
		t.Errorf("report %+v does not carry the rqlite error", r)
	}
}

// A tenant gateway binds the WireGuard IP, not localhost (chg-387); probing
// localhost reported every tenant gateway down.
func TestNamespaceGatewayHealthURL_usesTheBindAddress(t *testing.T) {
	got, err := namespaceGatewayHealthURL(nsInfo{name: "anchat", portBase: 10200, rqliteAddr: "10.0.0.1:10200"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://10.0.0.1:10204/v1/health" {
		t.Fatalf("got %q", got)
	}
	if _, err := namespaceGatewayHealthURL(nsInfo{name: "anchat", portBase: 10200}); err == nil {
		t.Fatal("no address accepted")
	}
}

// End to end: a gateway answering on the namespace's address is reported up.
func TestCollectNamespaceReport_probesGatewayOnItsAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	base := port - namespaceGatewayPortOffset
	ns := nsInfo{name: "anchat", portBase: base, rqliteAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(base))}

	r := collectNamespaceReport(ns, "", errors.New("rqlite not under test"))
	if !r.GatewayUp || r.GatewayStatus != http.StatusOK {
		t.Fatalf("gateway on the namespace address reported down: %+v", r)
	}
}
