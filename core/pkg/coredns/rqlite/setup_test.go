package rqlite

import (
	"strings"
	"testing"

	"github.com/coredns/caddy"
)

// rqlited binds only the node's WireGuard IP, so the plugin has no address it
// could default to: a Corefile without dsn is refused at parse time instead of
// polling localhost, where nothing listens.
func TestParseConfig_requiresDSN(t *testing.T) {
	c := caddy.NewTestController("dns", "rqlite {\n    refresh 5s\n}")
	_, err := parseConfig(c)
	if err == nil {
		t.Fatal("a Corefile without dsn was accepted")
	}
	if !strings.Contains(err.Error(), "dsn is required") {
		t.Fatalf("error %q does not say dsn is required", err)
	}
}
